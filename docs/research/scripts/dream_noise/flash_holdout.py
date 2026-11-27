"""Compare packed Flash plus pair confirmation with native dream on held-out synthetic facts."""

import argparse
import hashlib
import json
import os
from pathlib import Path

import experiment as base
import flash_confirm
import flash_packed
import pipeline


FIXTURE = base.HERE / "pipeline_holdout.json"
GROUPS_PER_REQUEST = 6


def classify_order(facts, groups, ask, key, order, noise_scores=None):
    def collect(name, request, cap, api_key):
        if name.startswith("flash-pairs-"):
            return None  # Collect the real router's anchors and neighbors before sending one packed prompt.
        return ask(name, request, cap, api_key)

    outline = pipeline.classify(facts, groups, collect, key, noise_scores=noise_scores, judge="flash")
    pairs = outline["pairs"]
    if order == "reversed":
        pairs = pairs[::-1]
    reply = flash_packed.ask_pairs(facts, pairs, GROUPS_PER_REQUEST, ask, f"flash-packed-{order}", key)
    return {
        **flash_packed.route_packed(facts, {"flash": outline, "groups": groups}, reply),
        "packed_order": [pair["anchor"] for pair in pairs],
    }


def run(cases, binary, budget, key, out):
    facts = [fact for case in cases for fact in case["facts"]]
    original = pipeline.compare_native(
        facts, binary,
        lambda groups: classify_order(facts, groups, budget.ask, key, "original"),
        lambda name, body, cap: budget.ask(name, body, cap, key),
        judge="flash",
    )
    reversed_class = classify_order(
        facts, original["groups"], budget.ask, key, "reversed", noise_scores=original["flash"]["noise"],
    )
    reuse_fallback = (
        reversed_class["escalate"] == original["flash"]["escalate"]
        and "error" not in (original.get("fallback") or {})
    )
    if reuse_fallback:
        reversed_run = {**original, "flash": reversed_class}
    else:
        reversed_run = pipeline.compare_native(
            facts, binary, lambda _groups: reversed_class,
            lambda name, body, cap: budget.ask(f"{name}-reversed", body, cap, key),
            previous=original, judge="flash",
        )
    reversed_run["fallback_reused"] = reuse_fallback
    runs = {name: pipeline.evaluate_result(cases, result)
            for name, result in (("original", original), ("reversed", reversed_run))}
    packed_path = out / "packed.json"
    packed_path.write_text(json.dumps(runs, indent=2) + "\n", encoding="utf-8")
    return flash_confirm.run_checks(cases, [(packed_path, runs)], budget.ask, key)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", type=Path, required=True)
    parser.add_argument("--out", type=Path, required=True)
    args = parser.parse_args()
    key = os.getenv("DEEPSEEK_API_KEY")
    if not key:
        parser.error("provide DEEPSEEK_API_KEY")
    cases = pipeline.read_cases(FIXTURE)
    binary = args.binary.resolve(strict=True)
    args.out.mkdir(mode=0o700, exist_ok=False)
    budget = base.Budget(args.out / "requests.json")
    result = run(cases, binary, budget, key, args.out)
    result["fixture_sha256"] = hashlib.sha256(FIXTURE.read_bytes()).hexdigest()
    (args.out / "report.json").write_text(json.dumps(result, indent=2) + "\n", encoding="utf-8")


if __name__ == "__main__":
    main()
