/* Regression driver: only public CLogan APIs produce the encrypted file. */
#include "clogan_core.h"
#include "clogan_status.h"
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
int main(int argc, char **argv) {
    if (argc != 4) return 2;
    int rows = atoi(argv[2]), interval = atoi(argv[3]);
    int init = clogan_init(argv[1], argv[1], 64 * 1024 * 1024,
                           "0123456789012345", "0123456789012345");
    if ((init != CLOGAN_INIT_SUCCESS_MMAP && init != CLOGAN_INIT_SUCCESS_MEMORY) ||
        clogan_open("native.bin") != CLOGAN_OPEN_SUCCESS) return 3;
    if (clogan_flush() != CLOGAN_FLUSH_SUCCESS) return 4;
    uint32_t rng = 0x12345678;
    char payload[4097];
    const char *alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
    for (int i = 0; i < rows; i++) {
        int prefix = snprintf(payload, sizeof(payload), "P26|large32|%08d|", i);
        for (int j = prefix; j < 4096; j++) {
            rng ^= rng << 13; rng ^= rng >> 17; rng ^= rng << 5;
            payload[j] = alphabet[rng & 63];
        }
        payload[4096] = 0;
        if (clogan_write(1, payload, 1792402017341LL + i, "main", 1, 1) != CLOGAN_WRITE_SUCCESS) return 5;
        if (interval && (i + 1) % interval == 0 && clogan_flush() != CLOGAN_FLUSH_SUCCESS) return 6;
    }
    for (int i = 0; i < 3; i++) if (clogan_flush() != CLOGAN_FLUSH_SUCCESS) return 7;
    return 0;
}
