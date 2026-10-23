#!/usr/bin/env python3
"""Generate synthetic encrypted Native bytes through actual public CLogan APIs."""
import argparse
import pathlib
import subprocess

p = argparse.ArgumentParser(description=__doc__)
p.add_argument('--output', type=pathlib.Path, required=True)
a = p.parse_args()
out = a.output.resolve()
if out.exists() and any(out.iterdir()):
    p.error("output must be a new empty directory; CLogan appends to existing files")
out.mkdir(parents=True, exist_ok=True)
module = pathlib.Path(__file__).resolve().parent.parent
native, mbed = module.parent/'Clogan', module.parent/'mbedtls'
args = ['cc', '-I', str(native), '-I', str(mbed/'include'),
        str(module/'tests/sdk/driver/native-workbench-producer.c')]
args += [str(native/(name+'.c')) for name in ('clogan_core','mmap_util','cJSON','construct_data',
        'zlib_util','json_util','aes_util','directory_util','base_util','console_util')]
args += [str(mbed/'library/aes.c'),str(mbed/'library/aesni.c'),'-lz','-o',str(out/'producer')]
subprocess.run(args, check=True)
subprocess.run([str(out/'producer'),str(out)], check=True)
print(out/'native.bin')
