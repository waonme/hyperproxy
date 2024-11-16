package main

import (
	"bytes"
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

	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	"go.opentelemetry.io/otel/attribute"
	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"

	"github.com/chai2010/webp"
	"github.com/labstack/echo/v4"
	"github.com/pkg/errors"
	"golang.org/x/image/draw"
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

	// setup cors
	c.Response().Header().Set("Access-Control-Allow-Origin", "*")
	c.Response().Header().Set("Access-Control-Allow-Methods", "GET")

	subpath := c.Param("*")

	cacheKeyBytes := sha256.Sum256([]byte(subpath))
	cacheKey := hex.EncodeToString(cacheKeyBytes[:])

	cachePath := filepath.Join(CachePath, cacheKey)

	if _, err := os.Stat(cachePath); err == nil {
		c.Response().Header().Set("Cache-Control", "public, max-age=86400")
		return c.File(cachePath)
	}

	splitter := strings.Index(subpath, "/")
	if splitter == -1 {
		err := errors.New("Failed to split the path")
		span.RecordError(err)
		return c.String(400, err.Error())
	}
	operator := subpath[:splitter]
	span.SetAttributes(attribute.String("operator", operator))

	split := strings.Split(operator, "x")
	if len(split) != 2 {
		err := errors.New("Failed to split the operator")
		span.RecordError(err)
		return c.String(400, err.Error())
	}
	widthStr := split[0]
	heightStr := split[1]

	width := 0
	if widthStr != "" {
		var err error
		width, err = strconv.Atoi(widthStr)
		if err != nil {
			err := errors.Wrap(err, "Failed to parse width")
			span.RecordError(err)
			return c.String(400, err.Error())
		}
	}

	height := 0
	if heightStr != "" {
		var err error
		height, err = strconv.Atoi(heightStr)
		if err != nil {
			err := errors.Wrap(err, "Failed to parse height")
			span.RecordError(err)
			return c.String(400, err.Error())
		}
	}

	remoteURL := subpath[splitter+1:]
	remoteURL = reCleanedURL.ReplaceAllString(remoteURL, "$1://$2")
	span.SetAttributes(attribute.String("remoteURL", remoteURL))
	originalCacheKeyBytes := sha256.Sum256([]byte(remoteURL))
	originalCacheKey := hex.EncodeToString(originalCacheKeyBytes[:])
	originalCachePath := filepath.Join(CachePath, originalCacheKey)

	var reader io.Reader

	// Check if the original image is already cached
	if _, err := os.Stat(originalCachePath); err == nil {
		reader, err = os.Open(originalCachePath)
		if err != nil {
			err := errors.Wrap(err, "Failed to open original cache")
			span.RecordError(err)
			return c.String(500, err.Error())
		}
	} else {

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

		targetIPs, err := net.LookupIP(parsedUrl.Host)
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

		_, fetchSpan := tracer.Start(ctx, "FetchImage")

		req, err := http.NewRequest("GET", remoteURL, nil)
		if err != nil {
			err := errors.Wrap(err, "Failed to create request")
			span.RecordError(err)
			return c.String(500, err.Error())
		}
		req.Header.Set("User-Agent", useragent)
		resp, err := client.Do(req)

		if err != nil {
			err := errors.Wrap(err, "Failed to fetch image")
			span.RecordError(err)
			return c.String(500, err.Error())
		}
		defer resp.Body.Close()

		fetchSpan.End()

		if resp.StatusCode != 200 {
			err := errors.New("Failed to fetch image")
			span.RecordError(err)
			return c.String(resp.StatusCode, err.Error())
		}

		// check if the image is valid
		if !strings.HasPrefix(resp.Header.Get("Content-Type"), "image/") {
			err := errors.New("Invalid image")
			span.RecordError(err)
			return c.String(400, err.Error())
		}

		// save the image to cache
		err = os.MkdirAll(CachePath, 0755)
		if err != nil {
			err := errors.Wrap(err, "Failed to create cache directory")
			span.RecordError(err)
			return c.String(500, err.Error())
		}

		cacheFile, err := os.Create(originalCachePath)
		if err != nil {
			err := errors.Wrap(err, "Failed to create cache file")
			span.RecordError(err)
			return c.String(500, err.Error())
		}
		defer cacheFile.Close()

		teeReader := io.TeeReader(resp.Body, cacheFile)

		reader = teeReader
	}

	// load image
	img, format, err := image.Decode(reader)
	if err != nil || format == "gif" {
		// fallback to original image

		err = os.MkdirAll(CachePath, 0755)
		if err != nil {
			err := errors.Wrap(err, "Failed to create cache directory")
			span.RecordError(err)
			return c.String(500, err.Error())
		}

		cacheFile, err := os.Create(cachePath)
		if err != nil {
			err := errors.Wrap(err, "Failed to create cache file")
			span.RecordError(err)
			return c.String(500, err.Error())
		}
		defer cacheFile.Close()

		_, err = io.Copy(cacheFile, reader)

		c.Response().Header().Set("Cache-Control", "public, max-age=86400")
		return c.File(originalCachePath)
	}

	originalWidth := img.Bounds().Dx()
	originalHeight := img.Bounds().Dy()

	resizeWidth := originalWidth
	resizeHeight := originalHeight

	// resize image
	if width != 0 && height != 0 {
		resizeWidth = width
		resizeHeight = height
	} else if width != 0 {
		resizeWidth = width
		resizeHeight = int(float64(width) / float64(originalWidth) * float64(originalHeight))
	} else if height != 0 {
		resizeHeight = height
		resizeWidth = int(float64(height) / float64(originalHeight) * float64(originalWidth))
	}

	if resizeWidth > originalWidth {
		resizeWidth = originalWidth
	}

	if resizeHeight > originalHeight {
		resizeHeight = originalHeight
	}

	_, resizeSpan := tracer.Start(ctx, "ResizeImage")

	dst := image.NewRGBA(image.Rect(0, 0, resizeWidth, resizeHeight))
	draw.NearestNeighbor.Scale(dst, dst.Bounds(), img, img.Bounds(), draw.Over, nil)

	resizeSpan.End()

	// encode image
	var buff bytes.Buffer
	err = webp.Encode(&buff, dst, &webp.Options{Quality: 80})
	if err != nil {
		err := errors.Wrap(err, "Failed to encode image")
		span.RecordError(err)
		return c.String(500, err.Error())
	}

	// save the image to cache
	err = os.MkdirAll(CachePath, 0755)
	if err != nil {
		err := errors.Wrap(err, "Failed to create cache directory")
		span.RecordError(err)
		return c.String(500, err.Error())
	}

	err = os.WriteFile(cachePath, buff.Bytes(), 0644)
	if err != nil {
		err := errors.Wrap(err, "Failed to write cache file")
		span.RecordError(err)
		return c.String(500, err.Error())
	}

	// return the image
	c.Response().Header().Set("Cache-Control", "public, max-age=86400")
	return c.Stream(200, "image/webp", &buff)
}
