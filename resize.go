package main

/*
#cgo pkg-config: Magick++
#include <stdlib.h>
#include "resize.h"
*/
import "C"
import (
	"fmt"
	"os/exec"
	"strings"
	"unsafe"

	"github.com/pkg/errors"
)

// init_resize: 既存の初期化ロジック
func init_resize(memoryLimit int) {
	if ret := C.init_resize(C.int(memoryLimit)); ret != 0 {
		panic("Failed to initialize resize (ImageMagick)")
	}
}

// advancedResize: 拡張子や品質に応じて C++ 側の advanced_resize を呼ぶ
//   - format: "JPEG"/"WEBP"/"PNG"/"GIF" など
func advancedResize(input, output string, width, height, quality int, format string) int {
	cInput := C.CString(input)
	defer C.free(unsafe.Pointer(cInput))

	cOutput := C.CString(output)
	defer C.free(unsafe.Pointer(cOutput))

	cFormat := C.CString(format)
	defer C.free(unsafe.Pointer(cFormat))

	ret := C.advanced_resize(
		cInput,
		cOutput,
		C.int(width),
		C.int(height),
		C.int(quality),
		cFormat,
	)
	return int(ret)
}

// resizeWithPngquant: "pngq" 用。まず PNG として出力し、pngquant で圧縮
func resizeWithPngquant(input, output string, width, height int, pngquantQuality string) error {
	// とりあえず PNG (品質=100) で書き出してから pngquant 実行
	ret := advancedResize(input, output, width, height, 100, "PNG")
	if ret != 0 {
		return errors.New("failed to resize image (pngq mode)")
	}

	// pngquant
	cmd := exec.Command("pngquant",
		"--force",
		"--output", output,
		"--quality", pngquantQuality, // e.g. "65-85"
		output,
	)
	if err := cmd.Run(); err != nil {
		return errors.Wrap(err, "pngquant failed")
	}

	return nil
}

// helper to convert extension -> (formatStr, quality)
func decideFormatAndQuality(ext string) (formatStr string, quality int) {
	// 環境変数で JPEG, WEBP の品質が設定されている
	// (image.go内で既に読み込まれている変数: jpegQuality, webpQuality)
	switch strings.ToLower(ext) {
	case "jpg", "jpeg":
		return "JPEG", jpegQuality
	case "webp":
		return "WEBP", webpQuality
	case "png":
		return "PNG", 100
	case "gif":
		return "GIF", 100
	default:
		return "WEBP", webpQuality // デフォルト
	}
}

// 追加: wrapper関数 (image.go内の呼び出し用)
func doResize(input, output string, width, height int, ext string) error {
	if ext == "pngq" {
		// pngquant対応
		return resizeWithPngquant(input, output, width, height, "65-85")
	} else {
		formatStr, q := decideFormatAndQuality(ext)
		ret := advancedResize(input, output, width, height, q, formatStr)
		if ret != 0 {
			return fmt.Errorf("failed to resize image: ext=%s", ext)
		}
		return nil
	}
}
