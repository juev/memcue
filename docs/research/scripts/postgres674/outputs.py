"""Create private research output without replacing prior evidence."""
import contextlib
import os
from pathlib import Path
import traceback


def output_path(path):
    out = Path(path).absolute()
    for component in (out, *out.parents):
        if component.is_symlink():
            raise FileExistsError('refuse symlink in output path')
    return out


def create_output_directory(path):
    out = output_path(path)
    out.mkdir(mode=0o700, parents=True, exist_ok=False)
    return out


def write_new_text(path, value):
    out = output_path(path)
    fd = os.open(out, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, 'w') as record:
        record.write(value)


@contextlib.contextmanager
def new_output_directory(path):
    out = create_output_directory(path)
    directory = os.open(out, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    try:
        yield out
    except BaseException:
        # The directory was created by this invocation. Never replace a record,
        # including a symlink or hardlink installed at the failure-record name.
        try:
            fd = os.open('failure-traceback.txt', os.O_WRONLY | os.O_CREAT | os.O_EXCL,
                         0o600, dir_fd=directory)
            with os.fdopen(fd, 'w') as record:
                record.write(traceback.format_exc())
        except OSError:
            pass  # Preserve the original exception and any prior failure record.
        raise
    finally:
        os.close(directory)
