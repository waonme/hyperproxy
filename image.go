package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/pkg/errors"
	"go.opentelemetry.io/otel/attribute"
)

const (
	CachePath    = "/tmp/hyperproxy"
	MaxCacheSize = 1024 * 1024 * 1024 // 1GB
)

var reCleanedURL = regexp.MustCompile(`^(https?):/+([^/])`)

func CleanDiskCache() int64 {

	err := os.MkdirAll(CachePath, 0755)
	if err != nil {
		err := errors.Wrap(err, "Failed to create cache directory")
		fmt.Println(err)
	}

	entries, err := os.ReadDir(CachePath)
	if err != nil {
		fmt.Println(err)
		return -1
	}

	files := make([]os.FileInfo, 0)

	totalSize := int64(0)
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			fmt.Println(err)
			continue
		}

		if info.IsDir() {
			continue
		}

		files = append(files, info)
		totalSize += info.Size()
	}

	if totalSize < MaxCacheSize {
		return totalSize
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].ModTime().Before(files[j].ModTime())
	})

	threadhold := 0.8 * MaxCacheSize

	for _, file := range files {
		if totalSize < int64(threadhold) {
			break
		}
		filePath := filepath.Join(CachePath, file.Name())
		err := os.Remove(filePath)
		if err != nil {
			fmt.Println(err)
			continue
		}
		fmt.Println("Evicted: ", filePath)
		totalSize -= file.Size()
	}

	return totalSize
}

func ImageHandler(c echo.Context) error {
	ctx, span := tracer.Start(c.Request().Context(), "ImageHandler")
	defer span.End()

	// CORS設定
	c.Response().Header().Set("Access-Control-Allow-Origin", "*")
	c.Response().Header().Set("Access-Control-Allow-Methods", "GET")

	// URL解析
	subpath := strings.TrimPrefix(c.Request().RequestURI, "/image/")
	splitter := strings.Index(subpath, "/")
	if splitter == -1 {
		err := errors.New("Invalid URL format: operator and remote URL must be separated by '/'")
		span.RecordError(err)
		return c.String(400, err.Error())
	}
	operator := subpath[:splitter]
	remoteURL := subpath[splitter+1:]

	// operatorの解析
	split := strings.Split(operator, "x")
	if len(split) != 2 {
		err := errors.New("Invalid operator format: must be [width]x[height][format]")
		span.RecordError(err)
		return c.String(400, err.Error())
	}
	widthStr := split[0]
	heightAndExtStr := split[1]

	var (
		outExtension = ""
		heightStr    = heightAndExtStr
	)
	// サポートされるフォーマット
	supportedFormats := []string{"webp", "png", "gif", "jpeg", "jpg"}
	for _, ext := range supportedFormats {
		if strings.HasSuffix(heightAndExtStr, ext) {
			outExtension = ext
			heightStr = strings.TrimSuffix(heightAndExtStr, ext)
			break
		}
	}

	// デフォルトはwebp
	if outExtension == "" {
		outExtension = "webp"
	}

	// 数値変換
	width, err := strconv.Atoi(widthStr)
	if err != nil {
		err := errors.Wrap(err, "Failed to parse width")
		span.RecordError(err)
		return c.String(400, err.Error())
	}
	height, err := strconv.Atoi(heightStr)
	if err != nil {
		err := errors.Wrap(err, "Failed to parse height")
		span.RecordError(err)
		return c.String(400, err.Error())
	}

	// IP制限処理
	parsedUrl, err := url.Parse(remoteURL)
	if err != nil {
		err := errors.Wrap(err, "Failed to parse URL")
		span.RecordError(err)
		return c.String(400, err.Error())
	}

	if parsedUrl.Scheme != "http" && parsedUrl.Scheme != "https" {
		err := errors.New("Invalid URL scheme")
		span.RecordError(err)
		return c.String(400, err.Error())
	}

	targetHost := parsedUrl.Host
	splitHost, _, err := net.SplitHostPort(parsedUrl.Host)
	if err == nil {
		targetHost = splitHost
	}

	targetIPs, err := net.LookupIP(targetHost)
	if err != nil {
		err := errors.Wrap(err, "Failed to lookup IP")
		span.SetAttributes(attribute.String("host", parsedUrl.Host))
		span.RecordError(err)
		return c.String(400, err.Error())
	}

	for _, denyIP := range denyIps {
		_, ipnet, err := net.ParseCIDR(denyIP)
		if err != nil {
			fmt.Println("Error parsing CIDR: ", err)
			span.RecordError(err)
			continue
		}

		for _, targetIP := range targetIPs {
			if ipnet.Contains(targetIP) {
				err := errors.New("IP is in deny list")
				span.RecordError(err)
				return c.String(403, err.Error())
			}
		}
	}

	// キャッシュキー生成
	originalCacheKeyBytes := sha256.Sum256([]byte(remoteURL))
	originalCacheKey := hex.EncodeToString(originalCacheKeyBytes[:])
	originalCachePath := filepath.Join(CachePath, originalCacheKey)

	requestCacheKeyBytes := sha256.Sum256([]byte(c.Request().RequestURI))
	requestCacheKey := hex.EncodeToString(requestCacheKeyBytes[:])
	requestCachePath := filepath.Join(CachePath, requestCacheKey) + "." + outExtension

	// キャッシュチェック
	if _, err := os.Stat(requestCachePath); err == nil {
		fmt.Println("  Cache hit")
		mimeType := getMimeType(outExtension)
		c.Response().Header().Set("Content-Type", mimeType)
		c.Response().Header().Set("Cache-Control", "public, max-age=86400, s-maxage=86400, immutable")
		return c.File(requestCachePath)
	}

	// 元画像取得またはキャッシュ
	if _, err := os.Stat(originalCachePath); err != nil {
		fmt.Println("  Fetch Original Image")
		err := fetchOriginalImage(remoteURL, originalCachePath)
		if err != nil {
			span.RecordError(err)
			return c.String(400, err.Error())
		}
	}

	// 特例処理: apngやsvg
	prefix := ""
	if strings.HasSuffix(remoteURL, ".apng") {
		prefix = "apng:"
	}

	if strings.HasSuffix(remoteURL, ".svg") || (width == 0 && height == 0) {
		fmt.Println("  Returning original image for SVG or no resizing needed")
		c.Response().Header().Set("Cache-Control", "public, max-age=86400, s-maxage=86400, immutable")
		c.Response().Header().Set("Content-Type", getMimeType(outExtension))
		return c.File(originalCachePath)
	}

	// リサイズ処理
	fmt.Println("  Resizing image")
	ok := resize(prefix+originalCachePath, requestCachePath, width, height, outExtension)
	if ok != 0 {
		err := errors.New("Failed to resize image")
		span.RecordError(err)
		return c.String(500, err.Error())
	}

	// レスポンス
	mimeType := getMimeType(outExtension)
	c.Response().Header().Set("Content-Type", mimeType)
	c.Response().Header().Set("Cache-Control", "public, max-age=86400, s-maxage=86400, immutable")
	return c.File(requestCachePath)
}
