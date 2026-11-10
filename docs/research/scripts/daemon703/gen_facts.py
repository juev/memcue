#!/usr/bin/env python3
"""Write N synthetic facts for `memcue add -batch` with the shape of a real base (#703).

The profile comes from aggregate statistics of one real text base (3737 active facts, 2026-11-10): content length
p10/p50/p90/p99 = 112/286/609/950 bytes, kinds and scope types in the proportions below, 45 repositories and
5 hosts. No real content is used. The output is the same for the same seed.

    python3 -I gen_facts.py 10000 facts.json
"""
import json
import random
import sys

KINDS = [('pitfall', 1254), ('environment', 1004), ('decision', 545), ('', 408), ('rule', 249),
         ('procedure', 121), ('event', 96)]
SCOPES = [('repo', 2025), ('host', 531), ('global', 517), ('shelf', 283), ('service', 280), ('', 72)]
REPOS = ['example.invalid/team/' + w for w in (
    'orchard quarry harbor beacon lantern compass meadow ridge canyon delta falcon garnet hollow '
    'island juniper kestrel lagoon marble nectar opal prairie quartz raven summit thistle umber '
    'violet willow yarrow zephyr anchor bramble cedar dune ember fjord glacier heron ivory jade '
    'kelp lotus maple nimbus').split()]
HOSTS = ['host-' + w for w in 'alpha bravo charlie delta echo'.split()]
SERVICES = ['service:' + w for w in 'postgres redis nginx vault kafka minio headscale grafana'.split()]

EN = ('the build fails when the cache directory is stale and the worker restarts before the lock is released '
      'deploy requires the release tag to match the version file otherwise the pipeline rejects the artifact '
      'use the configured launcher with an explicit base path and never rely on the default state directory '
      'migration runs in one transaction and refuses a schema that already holds rows from an earlier version '
      'timeout of the health check is ten seconds so a slow start marks the node unavailable '
      'rotate the certificate before it expires and restart the proxy to load the new chain '
      'the index rebuild takes about three minutes on the production base and blocks writes meanwhile').split()
RU = ('сборка падает когда кэш устарел и воркер перезапускается раньше чем снят лок '
      'деплой требует чтобы тег релиза совпадал с файлом версии иначе пайплайн отклоняет артефакт '
      'запускать только через настроенный лаунчер с явным путём к базе и не полагаться на каталог по умолчанию '
      'миграция идёт одной транзакцией и отказывает если схема уже содержит строки прошлой версии '
      'таймаут проверки здоровья десять секунд поэтому медленный старт помечает узел недоступным').split()
# Identifiers without versions: the add vet refuses version-bearing installation state outside a shelf.
IDENT = ('acme-api acme-jobs pgx /etc/nginx/nginx.conf MEMCUE_STATE_DIR TestParallelOpen make-lint '
         'sha256 --add-dir cron.daily internal/store recall.Search').split()


def weighted(rng, pairs):
    return rng.choices([v for v, _ in pairs], weights=[w for _, w in pairs])[0]


def length(rng):
    # Log-normal fitted to the real p50 286 and p90 609 bytes, clipped to the real p99 range.
    return max(40, min(1600, int(rng.lognormvariate(5.656, 0.59))))


def content(rng, target):
    words = EN if rng.random() < 0.7 else RU
    out = []
    while len(' '.join(out).encode()) < target:
        out.append(rng.choice(IDENT) if rng.random() < 0.08 else rng.choice(words))
    text = ' '.join(out)
    return text[0].upper() + text[1:] + '.'


def main():
    n, path = int(sys.argv[1]), sys.argv[2]
    rng = random.Random(703)
    facts = []
    for i in range(n):
        repo = rng.choice(REPOS)
        kind, scope = weighted(rng, KINDS), weighted(rng, SCOPES)
        scope = {'repo': 'repo:' + repo, 'host': 'host:' + rng.choice(HOSTS), 'shelf': 'shelf:research',
                 'service': rng.choice(SERVICES)}.get(scope, scope)
        facts.append({'Content': content(rng, length(rng)) + ' Case ' + str(i) + '.', 'Origin': repo + ' 2026-11-10',
                      # Source user: the installation-state vet applies to agent and tool facts only, and the
                      # source does not change storage or search cost.
                      'Kind': kind, 'Scope': scope, 'Source': 'user'})
    with open(path, 'w', encoding='utf-8') as f:
        json.dump(facts, f, ensure_ascii=False)


if __name__ == '__main__':
    main()
