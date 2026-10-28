"""Load the edge proxy configuration."""
import wrenfold


def load(path: str = "config/app.wf") -> dict:
    return wrenfold.load(path)
