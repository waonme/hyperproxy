
#include <Magick++.h>
#include <iostream>
#include <list>

#include "resize.h"

int init_resize(int memory_limit) {
    try {
        Magick::InitializeMagick(nullptr);
        Magick::ResourceLimits::memory(memory_limit);
    } catch (Magick::Exception &error) {
        std::cerr << "    Error: " << error.what() << std::endl;
        return 1;
    }

    return 0;
}

int resize(char* input_filename, char* output_filename, int targetWidth, int targetHeight) {

    std::cout << "  Input " << input_filename << std::endl;

    try {
        std::list<Magick::Image> frames;
        Magick::readImages(&frames, input_filename);

        std::cout << "    Frames found: " << frames.size() << std::endl;

        if (frames.empty()) {
            std::cerr << "    Error: No frames found in the input file." << std::endl;
            return 1;
        }

        std::list<Magick::Image> cFrames;
        Magick::coalesceImages(&cFrames, frames.begin(), frames.end());

        for (auto &frame : cFrames) {
            frame.autoOrient();

            if (targetWidth > frame.columns()) {
                targetWidth = frame.columns();
            }

            if (targetHeight > frame.rows()) {
                targetHeight = frame.rows();
            }

            frame.resize(Magick::Geometry(targetWidth, targetHeight));
            frame.quality(80);

            frame.magick("WEBP");
        }

        writeImages(cFrames.begin(), cFrames.end(), output_filename);

        std::cout << "    Done. Saved to " << output_filename << std::endl;
    } catch (Magick::Exception &error) {
        std::cerr << "    Error: " << error.what() << std::endl;
        return 1;
    }

    return 0;
}

