/* Test driver only: payload production is the repository's unchanged CLogan SDK. */
#include "clogan_core.h"
#include "clogan_status.h"
#include <stdio.h>
int main(int argc, char **argv) {
    if (argc != 4) return 2;
    int init = clogan_init(argv[1], argv[1], 10485760, argv[2], argv[3]);
    int opened = clogan_open("native.bin");
    int written = clogan_write(7, "SDK_CHAIN_NATIVE_20261016 中文 🌏 + percent%", 1792108800123LL, "sdk-fixture", 42, 1);
    int flushed = clogan_flush();
    printf("init=%d open=%d write=%d flush=%d\n", init, opened, written, flushed);
    return ((init != CLOGAN_INIT_SUCCESS_MMAP && init != CLOGAN_INIT_SUCCESS_MEMORY) || opened != CLOGAN_OPEN_SUCCESS || written != CLOGAN_WRITE_SUCCESS || flushed != CLOGAN_FLUSH_SUCCESS) ? 1 : 0;
}
