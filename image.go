package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/pkg/errors"
)

var (
	// 環境変数経由で初期化 (デフォルト85/80)
	jpegQuality = 85
	webpQuality = 80
)

func init() {
	if val := os.Getenv("JPEG_QUALITY"); val != "" {
		if q, err := strconv.Atoi(val); err == nil {
			jpegQuality = q
		}
	}
	if val := os.Getenv("WEBP_QUALITY"); val != "" {
		if q, err := strconv.Atoi(val); err == nil {
			webpQuality = q
		}
	}
}

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
		if err := os.Remove(filePath); err != nil {
			fmt.Println(err)
			continue
		}
		fmt.Println("Evicted: ", filePath)
		totalSize -= file.Size()
	}
	return totalSize
}

func getMimeType(extension string) string {
	switch extension {
	case "png", "pngq":
		return "image/png"
	case "webp":
		return "image/webp"
	case "jpeg", "jpg":
		return "image/jpeg"
	case "gif":
		return "image/gif"
	default:
		return "application/octet-stream"
	}
}

func fetchOriginalImage(remoteURL, filepath string) error {
	resp, err := http.Get(remoteURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Failed to fetch image: %s", resp.Status)
	}
	out, err := os.Create(filepath)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return err
}

// ImageHandler: /image/{operator}/{remoteURL}
func ImageHandler(c echo.Context) error {
	_, span := tracer.Start(c.Request().Context(), "ImageHandler")
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
		return c.String(http.StatusBadRequest, err.Error())
	}
	operator := subpath[:splitter]
	remoteURL := subpath[splitter+1:]

	// operatorの解析
	split := strings.Split(operator, "x")
	if len(split) != 2 {
		err := errors.New("Invalid operator format: must be [width]x[height][format]")
		span.RecordError(err)
		return c.String(http.StatusBadRequest, err.Error())
	}
	widthStr := split[0]
	heightAndExtStr := split[1]

	// 拡張子判定
	var (
		outExtension = ""
		heightStr    = heightAndExtStr
	)
	supportedFormats := []string{"webp", "png", "pngq", "gif", "jpeg", "jpg"}
	for _, ext := range supportedFormats {
		if strings.HasSuffix(heightAndExtStr, ext) {
			outExtension = ext
			heightStr = strings.TrimSuffix(heightAndExtStr, ext)
			break
		}
	}
	if outExtension == "" {
		// デフォルト
		outExtension = "webp"
	}

	width, err := strconv.Atoi(widthStr)
	if err != nil {
		span.RecordError(err)
		return c.String(http.StatusBadRequest, "Invalid width")
	}
	height, err := strconv.Atoi(heightStr)
	if err != nil {
		span.RecordError(err)
		return c.String(http.StatusBadRequest, "Invalid height")
	}

	// URLチェック
	parsedUrl, err := url.Parse(remoteURL)
	if err != nil {
		span.RecordError(err)
		return c.String(http.StatusBadRequest, "Invalid URL")
	}
	if parsedUrl.Scheme != "http" && parsedUrl.Scheme != "https" {
		span.RecordError(errors.New("Invalid URL scheme"))
		return c.String(http.StatusBadRequest, "Invalid URL scheme")
	}

	// IP制限 (denyIps は main.go で定義済み)
	targetHost := parsedUrl.Host
	splitHost, _, err := net.SplitHostPort(parsedUrl.Host)
	if err == nil {
		targetHost = splitHost
	}
	targetIPs, err := net.LookupIP(targetHost)
	if err != nil {
		span.RecordError(err)
		return c.String(http.StatusBadRequest, "Failed to resolve host")
	}
	for _, denyIP := range denyIps {
		_, ipnet, e2 := net.ParseCIDR(denyIP)
		if e2 != nil {
			continue
		}
		for _, tIP := range targetIPs {
			if ipnet.Contains(tIP) {
				return c.String(http.StatusForbidden, "Access denied for this IP")
			}
		}
	}

	// キャッシュパス生成
	originalCacheKeyBytes := sha256.Sum256([]byte(remoteURL))
	originalCacheKey := hex.EncodeToString(originalCacheKeyBytes[:])
	originalCachePath := filepath.Join(CachePath, originalCacheKey)

	requestCacheKeyBytes := sha256.Sum256([]byte(c.Request().RequestURI))
	requestCacheKey := hex.EncodeToString(requestCacheKeyBytes[:])
	requestCachePath := filepath.Join(CachePath, requestCacheKey) + "." + outExtension

	// すでにリサイズ後ファイルがあれば返す
	if _, err := os.Stat(requestCachePath); err == nil {
		mimeType := getMimeType(outExtension)
		c.Response().Header().Set("Content-Type", mimeType)
		return c.File(requestCachePath)
	}

	// 元画像がなければ取得
	if _, err := os.Stat(originalCachePath); err != nil {
		if ferr := fetchOriginalImage(remoteURL, originalCachePath); ferr != nil {
			span.RecordError(ferr)
			return c.String(http.StatusBadRequest, "Failed to fetch original image")
		}
	}

	// 特例処理: apngやsvg, or width=0,height=0
	prefix := ""
	if strings.HasSuffix(remoteURL, ".apng") {
		prefix = "apng:"
	}
	if strings.HasSuffix(remoteURL, ".svg") || (width == 0 && height == 0) {
		mimeType := getMimeType(outExtension)
		c.Response().Header().Set("Content-Type", mimeType)
		return c.File(originalCachePath)
	}

	// リサイズ実行
	if err := doResize(prefix+originalCachePath, requestCachePath, width, height, outExtension); err != nil {
		span.RecordError(err)
		return c.String(http.StatusInternalServerError, err.Error())
	}

	// 正常終了
	mimeType := getMimeType(outExtension)
	c.Response().Header().Set("Content-Type", mimeType)
	return c.File(requestCachePath)
}
