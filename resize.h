#ifndef RESIZE_H
#define RESIZE_H

#ifdef __cplusplus
extern "C" {
#endif

int init_resize(int memory_limit);

/**
 * advanced_resize:
 *  - input_filename: 入力ファイルパス
 *  - output_filename: 出力ファイルパス
 *  - targetWidth, targetHeight: リサイズ後の幅・高さ
 *  - quality: 画像の品質(0〜100程度)
 *  - format: "JPEG", "WEBP", "PNG", "GIF" 等 (大文字推奨)
 */
int advanced_resize(
    char* input_filename,
    char* output_filename,
    int targetWidth,
    int targetHeight,
    int quality,
    const char* format
);

#ifdef __cplusplus
}
#endif

#endif // RESIZE_H