"""Partial-result store: one JSON file per key under data_dir."""
import json
import os


class FileStore:
    def __init__(self, data_dir: str):
        self.data_dir = data_dir

    def put(self, key: str, value: dict) -> None:
        path = os.path.join(self.data_dir, key + ".json")
        tmp = path + ".tmp"
        with open(tmp, "w", encoding="utf-8") as f:
            json.dump(value, f)
            f.flush()
            os.fsync(f.fileno())
        os.replace(tmp, path)

    def get(self, key: str) -> dict | None:
        path = os.path.join(self.data_dir, key + ".json")
        try:
            with open(path, encoding="utf-8") as f:
                return json.load(f)
        except FileNotFoundError:
            return None
