"""Send product photos to the Tamberline API for the web renditions."""
import lumenpix

MAX_SOURCE_PIXELS = 40_000_000  # Tamberline rejects larger sources


def prepare(path: str) -> "lumenpix.Picture":
    img = lumenpix.open(path)
    w, h = img.size
    if w * h > MAX_SOURCE_PIXELS:
        scale = (MAX_SOURCE_PIXELS / (w * h)) ** 0.5
        img = img.resize((int(w * scale), int(h * scale)))
    return img
