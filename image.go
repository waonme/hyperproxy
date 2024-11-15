package main

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"

	"crypto/sha256"
	"path/filepath"

	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	"github.com/chai2010/webp"

	"github.com/labstack/echo/v4"

	"golang.org/x/image/draw"
)

const (
	CachePath    = "/tmp/hyperproxy"
	MaxCacheSize = 1024 * 1024 * 1024 // 1GB
)

func CleanDiskCache() {

	entries, err := os.ReadDir(CachePath)
	if err != nil {
		fmt.Println(err)
		return
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
		return
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].ModTime().Before(files[j].ModTime())
	})

	for _, file := range files {
		if totalSize < MaxCacheSize {
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
}

func ImageHandler(c echo.Context) error {

	subpath := c.Param("*")

	cacheKeyBytes := sha256.Sum256([]byte(subpath))
	cacheKey := hex.EncodeToString(cacheKeyBytes[:])

	cachePath := filepath.Join(CachePath, cacheKey)

	if _, err := os.Stat(cachePath); err == nil {
		return c.File(cachePath)
	}

	splitter := strings.Index(subpath, "/")
	if splitter == -1 {
		return c.String(400, "Bad Request")
	}
	operator := subpath[:splitter]

	split := strings.Split(operator, "x")
	if len(split) != 2 {
		return c.String(400, "Bad Request")
	}
	widthStr := split[0]
	heightStr := split[1]

	width := 0
	if widthStr != "" {
		var err error
		width, err = strconv.Atoi(widthStr)
		if err != nil {
			return c.String(400, "Bad Request")
		}
	}

	height := 0
	if heightStr != "" {
		var err error
		height, err = strconv.Atoi(heightStr)
		if err != nil {
			return c.String(400, "Bad Request")
		}
	}

	remoteURL := subpath[splitter+1:]

	originalCacheKeyBytes := sha256.Sum256([]byte(remoteURL))
	originalCacheKey := hex.EncodeToString(originalCacheKeyBytes[:])
	fmt.Println("originalCacheKey", originalCacheKey)

	originalCachePath := filepath.Join(CachePath, originalCacheKey)

	var reader io.Reader

	// Check if the original image is already cached
	if _, err := os.Stat(originalCachePath); err == nil {
		reader, err = os.Open(originalCachePath)
		if err != nil {
			return c.String(500, "Internal Server Error")
		}
	} else {

		parsedUrl, err := url.Parse(remoteURL)
		if err != nil {
			fmt.Println("Error parsing URL: ", err)
			return invalidURL(c, "Invalid URL", cacheKey)
		}

		targetIPs, err := net.LookupIP(parsedUrl.Host)
		if err != nil {
			fmt.Println("Error looking up IP: ", err)
			return invalidURL(c, parsedUrl.Host, cacheKey)
		}

		for _, denyIP := range denyIps {
			_, ipnet, err := net.ParseCIDR(denyIP)
			if err != nil {
				fmt.Println("Error parsing CIDR: ", err)
				continue
			}

			for _, targetIP := range targetIPs {
				if ipnet.Contains(targetIP) {
					fmt.Println("IP is in deny list: ", targetIP)
					return invalidURL(c, parsedUrl.Host, cacheKey)
				}
			}
		}

		req, err := http.NewRequest("GET", remoteURL, nil)
		if err != nil {
			return c.String(500, "Internal Server Error")
		}
		req.Header.Set("User-Agent", useragent)
		resp, err := client.Do(req)

		//resp, err := http.Get(remoteURL)
		if err != nil {
			return c.String(500, "Internal Server Error")
		}
		defer resp.Body.Close()

		if resp.StatusCode != 200 {
			return c.String(resp.StatusCode, "Internal Server Error")
		}

		// check if the image is valid
		if !strings.HasPrefix(resp.Header.Get("Content-Type"), "image/") {
			return c.String(400, "Bad Request")
		}

		// save the image to cache
		err = os.MkdirAll(CachePath, 0755)
		if err != nil {
			fmt.Println(err)
			return c.String(500, "Internal Server Error")
		}

		cacheFile, err := os.Create(originalCachePath)
		if err != nil {
			fmt.Println(err)
			return c.String(500, "Internal Server Error")
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
			return c.String(500, "Internal Server Error")
		}

		cacheFile, err := os.Create(cachePath)
		if err != nil {
			return c.String(500, "Internal Server Error")
		}
		defer cacheFile.Close()

		_, err = io.Copy(cacheFile, reader)

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

	dst := image.NewRGBA(image.Rect(0, 0, resizeWidth, resizeHeight))
	draw.NearestNeighbor.Scale(dst, dst.Bounds(), img, img.Bounds(), draw.Over, nil)

	// encode image
	var buff bytes.Buffer
	err = webp.Encode(&buff, dst, &webp.Options{Quality: 80})
	if err != nil {
		return c.String(500, "Internal Server Error")
	}

	// save the image to cache
	err = os.MkdirAll(CachePath, 0755)
	if err != nil {
		return c.String(500, "Internal Server Error")
	}

	err = os.WriteFile(cachePath, buff.Bytes(), 0644)
	if err != nil {
		return c.String(500, "Internal Server Error")
	}

	// return the image
	return c.Stream(200, "image/webp", &buff)
}
