"""Read-only local dependency probe. Never opens or copies passfile contents.

libpq alone consumes the caller-selected private passfile. No SQL is executed and
no model request is made. Endpoint details and error text stay in private output.
"""
import argparse
import ctypes
import ctypes.util
import json
import os
from pathlib import Path
import stat

from outputs import create_output_directory, write_new_text


def probe(endpoint_path, passfile, model_cache, dependency_discovery, out):
    out = create_output_directory(out)
    endpoint = json.loads(endpoint_path.read_text())
    if set(endpoint) != {'host', 'port', 'dbname', 'user', 'sslmode'}:
        raise ValueError('unexpected endpoint fields; credentials forbidden')
    info = passfile.lstat()
    if not stat.S_ISREG(info.st_mode) or info.st_mode & 0o077 or info.st_uid != os.getuid():
        raise ValueError('owned private regular passfile required')
    modules = json.loads(dependency_discovery.read_text())['runtime_dependencies']
    # This is metadata discovery only, not model loading or unverified encoding.
    model_files = {name: (model_cache/name).is_file()
                   for name in ['model.onnx', 'tokenizer.json', 'provenance.json']}
    library = ctypes.util.find_library('pq')
    result = dict(modules=modules, model_files=model_files,
                  passfile={'owned_regular_private': True, 'contents_opened_by_controller': False},
                  libpq_present=library is not None, pg_authenticated=False,
                  sql_statements=0, model_requests=0, connection_error=None)
    if library:
        pq = ctypes.CDLL(library)
        pq.PQconnectdbParams.argtypes = [ctypes.POINTER(ctypes.c_char_p), ctypes.POINTER(ctypes.c_char_p), ctypes.c_int]
        pq.PQconnectdbParams.restype = ctypes.c_void_p
        pq.PQstatus.argtypes = [ctypes.c_void_p]; pq.PQstatus.restype = ctypes.c_int
        pq.PQerrorMessage.argtypes = [ctypes.c_void_p]; pq.PQerrorMessage.restype = ctypes.c_char_p
        pq.PQserverVersion.argtypes = [ctypes.c_void_p]; pq.PQserverVersion.restype = ctypes.c_int
        pq.PQfinish.argtypes = [ctypes.c_void_p]
        fields = {**endpoint, 'passfile': str(passfile.absolute()), 'connect_timeout': '5',
                  'application_name': 'memcue674_synthetic_prerequisite'}
        keys = (ctypes.c_char_p*(len(fields)+1))(*[k.encode() for k in fields], None)
        values = (ctypes.c_char_p*(len(fields)+1))(*[str(v).encode() for v in fields.values()], None)
        connection = pq.PQconnectdbParams(keys, values, 0)
        if not connection: raise RuntimeError('libpq connection allocation failed')
        try:
            result['pg_authenticated'] = pq.PQstatus(connection) == 0
            result['server_version'] = pq.PQserverVersion(connection) if result['pg_authenticated'] else None
            if not result['pg_authenticated']:
                result['connection_error'] = pq.PQerrorMessage(connection).decode(errors='replace')
        finally:
            pq.PQfinish(connection)
    blockers = []
    if not result['pg_authenticated']: blockers.append('existing PostgreSQL endpoint could not be authenticated from this container')
    if not all(model_files.values()): blockers.append('existing local E5 cache is unavailable at the selected historical cache location')
    if not all(modules.values()): blockers.append('task-local PG/embedding Python dependencies are absent')
    result['blockers'] = blockers
    result['ready'] = not blockers
    write_new_text(out/'result.json', json.dumps(result, indent=2)+'\n')
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ['endpoint', 'passfile', 'model-cache', 'dependency-discovery', 'out']:
        parser.add_argument('--'+name, type=Path, required=True)
    args = parser.parse_args()
    result = probe(args.endpoint, args.passfile, args.model_cache, args.dependency_discovery, args.out)
    print(json.dumps({k: result[k] for k in ['ready', 'pg_authenticated', 'libpq_present', 'modules', 'model_files', 'blockers']}))
    return 0 if result['ready'] else 1


if __name__ == '__main__': raise SystemExit(main())
