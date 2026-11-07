"""Build a separately identified bridge; invoke through one build-check wrapper."""
import argparse
import hashlib
import json
from pathlib import Path
import subprocess
import sys
from outputs import new_output_directory, write_new_text
from source_identity import verify_source, verify_bridge

HERE = Path(__file__).resolve().parent


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--out', required=True, help='new private binary directory')
    args = parser.parse_args()
    identity = verify_source(HERE.parents[3])
    source_hash = hashlib.sha256((HERE / 'sqlite_bridge.go').read_bytes()).hexdigest()
    with new_output_directory(args.out) as out:
        argv = ['go', 'build', '-p', '2', '-ldflags',
                '-X main.productionIdentity=' + identity['production_digest'] +
                ' -X main.bridgeSourceSHA256=' + source_hash,
                '-o', str(out / 'bridge'), str(HERE / 'sqlite_bridge.go')]
        subprocess.run(argv, check=True)
        (out / 'bridge').chmod(0o700)
        record = dict(argv=argv, driver_argv=sys.orig_argv, source_identity=identity,
                      native_bridge=verify_bridge(out / 'bridge', identity, HERE / 'sqlite_bridge.go'),
                      go_build_info=subprocess.check_output(['go', 'version', '-m', str(out / 'bridge')], text=True))
        write_new_text(out / 'identity.json', json.dumps(record, indent=2)+'\n')
        print(json.dumps(record['native_bridge']))


if __name__ == '__main__': main()
