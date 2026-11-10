# Hardware illustrations

This candidate imports `nb1-web` commit `db3f0de9df6dc6552b8121542c0b00b007f3e783`, manifest SHA-256 `6a9c29bb9cf8f9c199aba30f1faf5fb76494e69f6a0407aaf130c1e7d19f7102`. The complete bilingual bundle contains 13 files and 259,987 bytes, leaving 2,157 bytes within the existing 256 KiB limit.

The debugger's original SVG artwork follows the MK2's continuous translucent housing, metal side and USB connector. The housing illustrations are labeled as simplified MK2 drawings. Shape references are the [official product photograph](https://blink1.thingm.com/wp-content/uploads/2019/03/blink1mk2-amazon-1024x829.png) and the [official repository photograph](https://github.com/todbot/blink1/blob/101abe3599c661c4dd4d1204b4d50fb592293143/docs/imgs/blink1mk2-twocolor-long.jpg).

The two readback swatches identify logical LED channels. Their screen positions do not represent the device's physical LED positions. USB RGB readback is distinct from a camera image and optical calibration. The correction changes HTML, CSS and translated labels; action requests, playback, recovery tickets and device ownership retain their existing behavior.

The running `6ca5a14e9f7fec8a5766e79f5bdef3fafef66d8f` installation and its hardware evidence remain a separate version. Importing this candidate's static assets does not install or switch that runtime.

## Reproduce the import

```sh
python3 tools/import-web-dist.py /path/to/nb1-web/dist \
  --source-commit db3f0de9df6dc6552b8121542c0b00b007f3e783 \
  --manifest-sha256 6a9c29bb9cf8f9c199aba30f1faf5fb76494e69f6a0407aaf130c1e7d19f7102 \
  --dry-run
```

Omitting `--dry-run` imports the verified bundle and updates its lock. The producer's `dist/` is built from committed source; the consumer never edits generated assets by hand.
