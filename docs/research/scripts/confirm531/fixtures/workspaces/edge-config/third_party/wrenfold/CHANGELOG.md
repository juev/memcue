# Wrenfold changelog

## 2.1.3 (2026-09-20)
- Fixed a crash on an empty file.

## 2.1.0 (2026-08-04)
- `@include` paths may use `${ENV}` variables.

## 2.0.0 (2026-06-01)
- New `@include "path"` directive: loads another `.wf` file at that point. A relative path is resolved against the
  including file. Includes nest up to 8 levels. A key set after an include overrides the included value.
- Removed the deprecated `!import` spelling.

## 1.4.2 (2026-02-11)
- Faster parsing of long arrays.
