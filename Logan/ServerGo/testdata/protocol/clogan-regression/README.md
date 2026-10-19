# CLogan loss regression

Synthetic host CLogan output generated using unchanged repository SDK via
`tests/sdk/generate.py --java-reference-rsa`. Only native public test material is
retained here. No RSA keys or real logs are included. AES key and IV are both
`0123456789012345`. See provenance.json for source and artifact SHA256.

native.bin contains the SDK internal header and the synthetic Chinese/emoji
marker. native-plain.bin was independently recovered by OpenSSL and gzip.
legacy-padded.gz is independently decrypted with OpenSSL -nopad, preserving each
frame's PKCS7 to reproduce historical stored objects. It is not a cloud capture.

The original Java golden and SDK sources remain unchanged. The Go recovery tests
assert all plaintext bytes, including both records, for new and historical forms.
