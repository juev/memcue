"""Offline tests for the bounded synthetic dream experiment."""

import io
import json
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))

import experiment
import flash_confirm
import flash_holdout
import flash_packed
import followup
import pipeline
import pipeline_native


def fact(identifier="f01", content="A synthetic record.", label="noise", phase="calibration"):
    return {"id": identifier, "content": content, "label": label, "phase": phase,
            "source": "agent", "kind": "other", "scope": "repo:sample"}


class Response:
    def __init__(self, body):
        self.body = io.BytesIO(json.dumps(body).encode())

    def __enter__(self):
        return self

    def __exit__(self, *args):
        self.body.close()

    def read(self, *args):
        return self.body.read(*args)


class ParseTests(unittest.TestCase):
    def test_jev_requires_typed_answers_and_bounded_numeric_probabilities(self):
        batch = [fact("f01"), fact("f02")]
        reply = {"answers": {
            "f01.one_off": {"type": "noul", "noul": 0.9},
            "f01.reusable": {"type": "noul", "noul": 0.1},
            "f01.choice": {"type": "choice", "choice": "noise",
                           "probabilities": {"noise": 0.8}},
            "f02.one_off": {"type": "score", "noul": 0.99},
            "f02.reusable": {"type": "noul", "noul": True},
            "f02.choice": {"type": "choice", "choice": "unknown",
                           "probabilities": {"unknown": 1.0}},
        }}

        self.assertEqual(experiment.parse_jev(reply, batch), {
            "f01": {"one_off": 0.9, "reusable": 0.1, "choice": "noise", "score": 0.8},
            "f02": {"one_off": None, "reusable": None, "choice": None, "score": None},
        })
        self.assertTrue(all(value is None for value in experiment.parse_jev(None, batch)["f01"].values()))

    def test_flash_accepts_only_complete_json_with_allowed_labels(self):
        batch = [fact("f01"), fact("f02")]
        valid = {"choices": [{"finish_reason": "stop", "message": {
            "content": '{"f01":"noise","f02":"wrong"}'}}]}
        self.assertEqual(experiment.parse_flash(valid, batch), {"f01": "noise", "f02": None})
        for malformed in (None, {}, {"choices": []},
                          {"choices": [{"finish_reason": "length", "message": {"content": "{}"}}]},
                          {"choices": [{"finish_reason": "stop", "message": {"content": "[]"}}]},
                          {"choices": [{"finish_reason": "stop", "message": {"content": "{"}}]}):
            with self.subTest(malformed=malformed):
                self.assertEqual(experiment.parse_flash(malformed, batch), {"f01": None, "f02": None})


class ThresholdTests(unittest.TestCase):
    def test_noul_requires_both_signals_and_no_threshold_is_inconclusive(self):
        strong_noise = {"one_off": 0.8, "reusable": 0.1, "choice": None, "score": None}
        mixed = {"one_off": 0.95, "reusable": 0.9, "choice": None, "score": None}
        uncertain = {"one_off": None, "reusable": 0.0, "choice": None, "score": None}
        self.assertIs(experiment.nominate(strong_noise, "noul", (0.8, 0.1)), True)
        self.assertIs(experiment.nominate(mixed, "noul", (0.8, 0.1)), False)
        self.assertIsNone(experiment.nominate(uncertain, "noul", (0.8, 0.1)))
        self.assertIs(experiment.nominate(strong_noise, "noul", None), False)
        self.assertIsNone(experiment.nominate(uncertain, "noul", None))

    def test_tune_rejects_false_positives_and_uses_deterministic_tie_break(self):
        rows = [fact("f01", label="noise"), fact("f02", label="noise"),
                fact("f03", label="useful")]
        scores = {
            "f01": {"one_off": 0.8, "reusable": 0.1},
            "f02": {"one_off": 0.8, "reusable": 0.1},
            "f03": {"one_off": 0.7, "reusable": 0.1},
        }
        self.assertEqual(experiment.tune(rows, scores, "noul"), (0.8, 0.1))
        self.assertIsNone(experiment.tune(rows, {
            "f01": {"one_off": 0.9, "reusable": 0.1},
            "f02": {"one_off": 0.0, "reusable": 0.0},
            "f03": {"one_off": 0.9, "reusable": 0.1},
        }, "noul"))

    def test_choice_tuning_uses_strictest_tied_threshold(self):
        rows = [fact("f01", label="noise"), fact("f02", label="useful")]
        scores = {
            "f01": {"choice": "noise", "score": 0.95},
            "f02": {"choice": "useful", "score": 1.0},
        }
        self.assertEqual(experiment.tune(rows, scores, "choice"), (0.95,))


class PayloadTests(unittest.TestCase):
    def test_provider_payloads_expose_fact_text_and_ids_but_not_gold_labels(self):
        batch = [fact("f01", "Synthetic one-off run."),
                 fact("f02", "Synthetic reusable rule.", label="useful", phase="control")]
        jev = experiment.jev_request(batch)
        flash = experiment.flash_request(batch)
        self.assertEqual(jev["state"], {"facts": {"f01": "Synthetic one-off run.",
                                                   "f02": "Synthetic reusable rule."}})
        flash_text = json.dumps(flash)
        for payload in (jev, flash):
            rendered = json.dumps(payload)
            self.assertNotIn('"label"', rendered)
            self.assertNotIn('"phase"', rendered)
            self.assertNotIn('"source"', rendered)
            self.assertNotIn('"kind"', rendered)
            self.assertNotIn('"scope"', rendered)
        self.assertIn("Synthetic one-off run.", flash_text)


class BudgetTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.budget = experiment.Budget(Path(self.temp.name) / "requests.json")

    def test_timeout_keeps_reserved_peak_spend_and_call_slot(self):
        with patch.object(experiment, "urlopen", side_effect=TimeoutError):
            result = self.budget.ask("flash-calibration-1", experiment.flash_request([fact()]), 512, "key")

        self.assertIsNone(result)
        self.assertEqual(len(self.budget.calls), 1)
        call = self.budget.calls[0]
        self.assertEqual(call["state"], "error")
        self.assertEqual(call["error"], "TimeoutError")
        self.assertGreater(call["reserved"], 0)
        self.assertEqual(json.loads(self.budget.journal.read_text()), self.budget.calls)
        with patch.object(experiment, "MAX_USD", call["reserved"] * 1.5), \
                patch.object(experiment, "urlopen") as urlopen:
            with self.assertRaisesRegex(ValueError, "budget exhausted before flash-retry"):
                self.budget.ask("flash-retry", experiment.flash_request([fact()]), 512, "key")
        urlopen.assert_not_called()

    def test_budget_rejected_before_network_call(self):
        self.budget.calls = [{"reserved": experiment.MAX_USD, "spend_bound": experiment.MAX_USD}]
        with patch.object(experiment, "urlopen") as urlopen:
            with self.assertRaisesRegex(ValueError, "budget exhausted before flash-control-1"):
                self.budget.ask("flash-control-1", experiment.flash_request([fact()]), 512, "key")
        urlopen.assert_not_called()

    def test_usage_is_recorded_and_peak_bound_covers_actual_jev_cost(self):
        response = {"model": "jev-1.14.0", "answers": {},
                    "usage": {"input_tokens": 120, "output_tokens": 8}}
        with patch.object(experiment, "urlopen", return_value=Response(response)) as urlopen:
            result = self.budget.ask("jev-calibration-1", experiment.jev_request([fact()]), 0, "key")

        self.assertEqual(result, response)
        self.assertEqual(urlopen.call_args.kwargs["timeout"], 180)
        call = self.budget.calls[0]
        self.assertEqual(call["state"], "answered")
        self.assertEqual(call["returned_model"], "jev-1.14.0")
        self.assertEqual(call["input"], 120)
        self.assertGreaterEqual(call["spend_bound"], call["usd"])
        self.assertEqual(json.loads(self.budget.journal.read_text()), self.budget.calls)

    def test_missing_key_does_not_reserve_or_call_provider(self):
        with patch.object(experiment, "urlopen") as urlopen:
            self.assertIsNone(self.budget.ask("jev-calibration-1", experiment.jev_request([fact()]), 0, ""))
        urlopen.assert_not_called()
        self.assertEqual(self.budget.calls, [])


class FollowupTests(unittest.TestCase):
    def test_followup_isolates_jev_questions_and_repeats_the_same_flash_request(self):
        calibration = [row for row in experiment.read_fixture() if row["phase"] == "calibration"]
        holdout = followup.read_holdout()
        calls = {}

        def missing_reply(name, body, cap, _key):
            calls[name] = (body, cap)

        with patch.object(experiment, "urlopen") as urlopen:
            report = followup.run(calibration, holdout, missing_reply, "jev", "flash")
        urlopen.assert_not_called()
        self.assertEqual(len(calls), experiment.MAX_CALLS)
        for name, (body, _) in calls.items():
            if name.startswith("jev-"):
                variant = name.split("-")[1]
                self.assertEqual({key.split(".")[1] for key in body["questions"]}, {variant})
                self.assertEqual(len(body["questions"]), len(body["state"]["facts"]))
        for index in (1, 2):
            none, cap = calls[f"flash-none-{index}"]
            self.assertEqual(calls[f"flash-none_repeat-{index}"], (none, cap))
            for effort in ("low", "high"):
                body, effort_cap = calls[f"flash-{effort}-{index}"]
                self.assertEqual(body, {**none, "thinking": {"type": "enabled"}, "reasoning_effort": effort})
                self.assertEqual(effort_cap, cap)
        self.assertIsNone(report["jev"]["reusable"]["threshold"])
        self.assertEqual(len(report["jev"]["reusable"]["holdout"]["missing"]), len(holdout))

    def test_reusable_threshold_prefers_the_strictest_zero_false_positive_cutoff(self):
        rows = [fact("f01", label="noise"), fact("f02", label="uncertain")]
        scores = {"f01": {"reusable": 0.08}, "f02": {"reusable": 0.25}}
        self.assertEqual(followup.tune(rows, scores, "reusable"), 0.10)


class PipelineTests(unittest.TestCase):
    def test_flash_pairs_use_explicit_criteria_and_only_screened_same_project_neighbors(self):
        facts = [
            {**fact("f01", "Existing rule: use port 443"), "scope": "repo:demo"},
            {**fact("f02", "Use port 443"), "scope": "repo:demo"},
            {**fact("f03", "Noise neighbor"), "scope": "repo:demo"},
            {**fact("f04", "Use port 443 elsewhere"), "scope": "repo:other"},
        ]
        calls = []

        def ask(name, body, cap, key):
            self.assertEqual(key, "stub")
            self.assertEqual(cap, 512)
            calls.append((name, body))
            if name.startswith("flash-noise-"):
                labels = {"f01": "useful", "f02": "useful", "f03": "noise", "f04": "useful"}
            else:
                labels = {f"f02.{kind}": "yes" if kind == "covered" else "no"
                          for kind in pipeline.QUESTIONS}
            return {"choices": [{"finish_reason": "stop", "message": {"content": json.dumps(labels)}}]}

        result = pipeline.classify(facts, [["f01", "f02", "f03", "f04"]], ask, "stub", judge="flash")

        self.assertEqual([name for name, _ in calls], ["flash-noise-1", "flash-pairs-1-1"])
        pair_request = calls[1][1]
        self.assertEqual(pair_request["model"], experiment.FLASH_MODEL)
        self.assertEqual(pair_request["thinking"], {"type": "disabled"})
        user_message = json.loads(pair_request["messages"][1]["content"])
        self.assertEqual(user_message["state"]["existing_fact"], facts[0]["content"])
        questions = user_message["questions"]
        self.assertEqual(set(questions), {f"f02.{kind}" for kind in pipeline.QUESTIONS})
        self.assertIn("Every reusable claim", questions["f02.covered"]["criteria"]["true"])
        self.assertEqual(result["pairs"][0]["scores"]["f02"]["covered"], 1.0)
        self.assertEqual(result["actions"], [pipeline.retire("f03", "noise"),
                                             pipeline.retire("f02", "duplicate", "f01")])
        self.assertEqual(result["screened"], ["f03"])
        self.assertEqual(result["compared"], ["f01", "f02"])

    def test_flash_missing_pair_label_routes_to_incomplete(self):
        facts = [{**fact("f01", "Existing rule"), "scope": "repo:demo"},
                 {**fact("f02", "Possible duplicate"), "scope": "repo:demo"}]

        def ask(name, _body, _cap, _key):
            if name.startswith("flash-noise-"):
                labels = {"f01": "useful", "f02": "useful"}
            else:
                labels = {f"f02.{kind}": "no" for kind in pipeline.QUESTIONS if kind != "covered"}
            return {"choices": [{"finish_reason": "stop", "message": {"content": json.dumps(labels)}}]}

        result = pipeline.classify(facts, [["f01", "f02"]], ask, "stub", judge="flash")

        self.assertEqual(result["pairs"][0]["routes"], {"f02": "incomplete"})
        self.assertEqual(result["actions"], [])

    def test_noise_leaves_group_before_pair_check_and_projects_stay_separate(self):
        facts = [fact("f01", "confirmed noise"), fact("f02", "old rule"), fact("f03", "old rule rephrased"),
                 dict(fact("f04", "rule in a different project"), scope="repo:other")]
        payloads = []

        def ask(name, payload, _cap, key):
            self.assertEqual(key, "stub")
            payloads.append(payload)
            if name.startswith("jev-noise"):
                labels = {"f01": "noise", "f02": "useful", "f03": "useful", "f04": "useful"}
                return {"answers": {f"{k}.choice": {"type": "choice", "choice": v, "probabilities": {v: 0.99}}
                                    for k, v in labels.items()}}
            return {"answers": {f"f03.{k}": {"type": "noul", "noul": 0.99 if k == "covered" else 0.01}
                                for k in pipeline.QUESTIONS}}

        result = pipeline.classify(facts, [["f01", "f02", "f03", "f04"]], ask, "stub")
        self.assertEqual(result["actions"], [pipeline.retire("f01", "noise"), pipeline.retire("f03", "duplicate", "f02")])
        self.assertEqual(result["escalate"], [])
        self.assertEqual(payloads[1]["state"], {"existing_fact": facts[1]["content"]})
        for question in payloads[1]["questions"].values():
            self.assertEqual(question["instructions"]["neighbor"], facts[2]["content"])

    def test_unresolved_conflict_blocks_duplicate_or_merge(self):
        scores = dict.fromkeys(pipeline.QUESTIONS, 0.01)
        scores.update(covered=0.95, complement=0.95, conflict=0.95)
        self.assertEqual(pipeline.route_pair(scores), "keep")
        self.assertEqual(pipeline.route_pair({**scores, "replaced": 0.95}), "outdated")
        self.assertEqual(pipeline.route_pair({**scores, "replaced": None}), "incomplete")

    def test_correct_count_requires_preservation_of_expected_terms(self):
        expected = [{"id": "f01", "op": "correct", "terms": ["port 443", "v2"]}]
        action = {"id": "f01", "op": "correct", "content": "Connect to port 443."}
        result = pipeline.evaluate(expected, [action])
        self.assertEqual(result["missed"], expected)
        self.assertEqual(result["unexpected"], [action])
        self.assertEqual(pipeline.evaluate(expected, [{**action, "content": "Use v2 on port 443."}])["missed"], [])

    def test_fallback_excludes_unselected_members_of_the_same_group(self):
        text = ('Packet prefix\n'
                '\n## Data: splitting\n\n```json\n{"facts":[1]}\n```\n\n## Data: scope\n\n'
                '## Group 1\n\n- id old (origin: synthetic)\n  Keep this text.\n'
                '- id noise (origin: synthetic)\n  Omit this text.\n')
        result = pipeline_native._filtered_text(text, {"old"})
        self.assertIn('{"facts":null}', result)
        self.assertIn("Keep this text.", result)
        self.assertNotIn("Omit this text.", result)


class PackedFlashTests(unittest.TestCase):
    def test_packed_request_contains_only_pair_text_and_shared_criteria(self):
        facts = [
            {**fact("f01", "Anchor one"), "private": "metadata-one"},
            {**fact("f02", "Neighbor one"), "private": "metadata-two"},
            {**fact("f03", "Anchor two"), "private": "metadata-three"},
            {**fact("f04", "Neighbor two"), "private": "metadata-four"},
        ]
        pairs = [
            {"anchor": "f01", "scores": {"f02": 0.9}},
            {"anchor": "f03", "scores": {"f04": 0.8}},
        ]

        request = flash_packed.packed_request(facts, pairs)
        payload = json.loads(request["messages"][1]["content"])

        self.assertEqual(request["thinking"], {"type": "disabled"})
        self.assertEqual(set(payload), {"criteria", "groups"})
        self.assertEqual(payload["criteria"], {
            key: {"question": question, "true": yes, "false": no}
            for key, (question, yes, no) in pipeline.QUESTIONS.items()
        })
        self.assertEqual(payload["groups"], [
            {"anchor": {"id": "f01", "content": "Anchor one"},
             "neighbors": [{"id": "f02", "content": "Neighbor one"}]},
            {"anchor": {"id": "f03", "content": "Anchor two"},
             "neighbors": [{"id": "f04", "content": "Neighbor two"}]},
        ])
        self.assertEqual(
            json.loads(flash_packed.packed_request(facts, list(reversed(pairs)))
                       ["messages"][1]["content"])["groups"],
            list(reversed(payload["groups"])),
        )
        serialized = request["messages"][1]["content"]
        for marker in ("metadata-one", "metadata-two", "metadata-three", "metadata-four",
                       '"label"', '"phase"', '"source"', '"scope"'):
            self.assertNotIn(marker, serialized)

    def test_chunk_response_cannot_overwrite_another_chunks_label(self):
        facts = [fact(f"f{i:02}", f"Record {i}") for i in range(1, 5)]
        pairs = [{"anchor": "f01", "scores": {"f02": {}}}, {"anchor": "f03", "scores": {"f04": {}}}]
        replies = iter(({"f02.covered": "no"}, {"f02.covered": "yes", "f04.covered": "yes"}))

        def ask(_name, _request, _cap, _key):
            return {"choices": [{"finish_reason": "stop", "message": {"content": json.dumps(next(replies))}}]}

        combined = flash_packed.ask_pairs(facts, pairs, 1, ask, "test", "stub")
        self.assertEqual(experiment.flash_object(combined), {"f02.covered": "no", "f04.covered": "yes"})


class HoldoutFlashTests(unittest.TestCase):
    def test_reversed_packed_groups_are_sent_once_after_noise_is_removed(self):
        facts = [
            fact("f01", "First anchor", label="useful"),
            fact("f02", "Noise neighbor"),
            fact("f03", "Second anchor", label="useful"),
            fact("f04", "Second neighbor", label="useful"),
            fact("f05", "Third anchor", label="useful"),
            fact("f06", "Third neighbor", label="useful"),
        ]
        groups = [["f01", "f02"], ["f03", "f04"], ["f05", "f06"]]
        noise = {row["id"]: {
            "choice": "noise" if row["id"] == "f02" else "useful",
            "score": 1.0 if row["id"] == "f02" else 0.0,
        } for row in facts}
        calls = []

        def ask(name, body, cap, key):
            calls.append((name, body, cap, key))
            self.assertEqual(name, "flash-packed-reversed")
            self.assertEqual(cap, 1200)
            return {"choices": [{"finish_reason": "stop", "message": {"content": json.dumps({
                f"{identifier}.{kind}": "no"
                for identifier in ("f04", "f06") for kind in pipeline.QUESTIONS
            })}}]}

        result = flash_holdout.classify_order(facts, groups, ask, "stub", "reversed", noise_scores=noise)

        self.assertEqual(len(calls), 1)
        name, request, _, _ = calls[0]
        self.assertEqual(name, "flash-packed-reversed")
        self.assertEqual(request["thinking"], {"type": "disabled"})
        payload = json.loads(request["messages"][1]["content"])
        self.assertEqual([group["anchor"]["id"] for group in payload["groups"]], ["f05", "f03"])
        self.assertEqual(result["packed_order"], ["f05", "f03"])
        self.assertEqual(result["screened"], ["f02"])
        self.assertNotIn("f02", json.dumps(payload))
        self.assertEqual(result["actions"], [pipeline.retire("f02", "noise")])


class FlashConfirmationTests(unittest.TestCase):
    def test_confirmation_request_contains_only_pair_text_and_kind_specific_rule(self):
        record = {**fact("f01", "Retained memory", label="useful"), "private": "record-secret"}
        evidence = {**fact("f02", "Supporting memory", label="noise"), "private": "evidence-secret"}

        for kind in ("duplicate", "outdated", "resolved"):
            with self.subTest(kind=kind):
                action = {"id": "f01", "op": "retire", "kind": kind, "by": "f02"}
                request = flash_confirm.confirmation_request(action, {"f01": record, "f02": evidence})
                self.assertEqual(request["thinking"], {"type": "disabled"})
                self.assertEqual(json.loads(request["messages"][1]["content"]), {
                    "record": record["content"], "evidence": evidence["content"],
                })
                rendered = json.dumps(request)
                for marker in ("record-secret", "evidence-secret", '"label"', '"phase"',
                               '"source"', '"kind"', '"scope"'):
                    self.assertNotIn(marker, rendered)
                for other_kind, criterion in flash_confirm.CRITERIA.items():
                    if other_kind == kind:
                        self.assertIn(criterion, request["messages"][0]["content"])
                    else:
                        self.assertNotIn(criterion, request["messages"][0]["content"])

    def test_only_explicitly_confirmed_pair_retirements_survive(self):
        duplicate = {"id": "f01", "op": "retire", "kind": "duplicate", "by": "f02"}
        outdated = {"id": "f03", "op": "retire", "kind": "outdated", "by": "f04"}
        resolved = {"id": "f05", "op": "retire", "kind": "resolved", "by": "f06"}
        noise = {"id": "f07", "op": "retire", "kind": "noise"}
        correct = {"id": "f08", "op": "correct", "content": "Updated text"}
        actions = [duplicate, outdated, resolved, noise, correct]
        verdicts = {
            flash_confirm.pair_key(duplicate): "confirm",
            flash_confirm.pair_key(outdated): "reject",
            flash_confirm.pair_key(resolved): "uncertain",
        }

        self.assertEqual(flash_confirm.pair_key(noise), None)
        self.assertEqual(flash_confirm.pair_key(correct), None)
        self.assertEqual(flash_confirm.confirmed_actions(actions, verdicts), [duplicate, noise, correct])
        self.assertEqual(flash_confirm.confirmed_actions(actions, {}), [noise, correct])

    def test_noise_only_run_skips_pair_confirmation(self):
        noise = pipeline.retire("f01", "noise")
        cases = [{"facts": [{"id": "f01"}], "expected": [noise]}]
        run = pipeline.evaluate_result(cases, {
            "judge": "flash", "fixture_facts": 1, "full": {}, "flash": {"actions": [noise]},
        })

        def ask(*_args):
            self.fail("noise must not trigger a pair confirmation request")

        result = flash_confirm.run_checks(cases, [("fixture", {"original": run})], ask, "")

        self.assertEqual(result["checks"], [])
        self.assertEqual(result["variants"][0]["after"]["correct"], [noise])


if __name__ == "__main__":
    unittest.main()
