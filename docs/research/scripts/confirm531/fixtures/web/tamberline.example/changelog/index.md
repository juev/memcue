# Tamberline image API: changelog

## v2.6 (2026-09-14)
- Source images up to 100 megapixels are accepted; larger sources are still rejected with HTTP 413.
- The output size limit is unchanged: 8,000 px on the long side.

## v2.5 (2026-07-22)
- New output format `format=avif`.
- `fit=stretch` is deprecated and will be removed in v3.

## v2.4 (2026-04-30)
- Faster JPEG decoding.
