#ifndef RESIZE_H
#define RESIZE_H


#ifdef __cplusplus
extern "C" {
#endif

int init_resize(int memory_limit);
int resize(char* input_filename, char* output_filename, int targetWidth, int targetHeight);

#ifdef __cplusplus
}
#endif

#endif // RESIZE_H
