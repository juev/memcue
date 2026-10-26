# Where the time of `dream -run -all` goes, October 2026

Status: **measured 2026-10-26 for [#471](https://github.com/juev/memcue/issues/471); the change to ship is not chosen
yet.** The full run of 2026-10-25 took 191 min for 106 packets. This note splits a packet's time and measures four
ways to cut it on saved packets, without a full run of the model over the base.

## Setup

- A copy of the Claude Code base of 2026-10-26 (3,602 live facts) with the debt of 2026-10-25 put back: every
  translation removed, the search queries of 3,093 facts removed, no dream state. Its packets have the shape of the
  2026-10-25 run: 40 fresh facts, 30–36 to expand, 40 to translate, 6–21 groups, 70–107 KB.
- Six passes one after another with the binary of this branch, which now logs `timing` and the `claude -p` usage in
  `dream.log`. The six packets were saved and sent again with `docs/research/scripts/dreamtime`, the same call as
  `dream -run`, under other settings. One run per cell.

## Time of one packet

| pass | prepare | model | of it to the first token | apply | output tokens (thinking) |
|---|---|---|---|---|---|
| 1 | 0.8 s | 96.7 s | 38.7 s | 0.2 s | 11,086 (3,433) |
| 2 | 1.8 s | 64.4 s | 14.7 s | 0.1 s | 6,684 (896) |
| 3 | 2.9 s | 135.9 s | 80.2 s | 0.2 s | 7,920 (1,573) |
| 4 | 2.9 s | 101.2 s | 48.2 s | 0.2 s | 10,482 (4,155) |
| 5 | 2.2 s | 71.4 s | 16.8 s | 0.1 s | 8,161 (1,569) |
| 6 | 2.8 s | 69.8 s | 14.7 s | 0.1 s | 7,683 (1,198) |

- The binary's own work is 1–3 % of a pass. The model call is the rest.
- After the first token the model writes for 50–58 s in every pass: about 6,000 visible tokens, at 110–130 a second.
  Of the answer's text, translations are 45–60 % and search queries 35–45 %. Splits and group actions are small.
- The wait for the first token is 15–80 s and follows the thinking tokens only loosely: pass 3 waited 80 s for 1,573
  of them. A later call waited 64 s with 34 thinking tokens. Part of that wait is on the API side.

## What each change did

**Packets in parallel.** The six packets sent at once finished in 95 s, against 540 s one after another; each call
took 75–95 s, as alone. Six calls at once hit no limit. Same cost.

**One packet split by task.** Three calls at once per packet: translation, search queries, and splits with groups.
A pass took 30.5 s and 32.6 s on packets 2 and 4, and 91.6 s on packet 6, where the translation call waited 64 s for
its first token. The translation and query calls think 34–133 tokens; the call with splits and groups 900–1,800.
Cost rose from $0.43–0.56 to $0.83–0.91 a packet, because each call carries the whole instructions.

**Lower effort and smaller models**, packets 2, 4, 6:

| | wall | to the first token | what changed in the answer against Opus |
|---|---|---|---|
| Opus, `--effort low` | 54–61 s | 5–7 s | 2 of 4 splits gone |
| Sonnet 5.5 | 38–53 s | 10–11 s | packet 4: queries for 12 facts of 35; 3 of 4 splits gone; 2 group actions |
| Haiku 5.5 | 163–271 s | 143–250 s | 35,000–60,000 thinking tokens; 2–7 group actions per packet |

Splits and group actions vary between two Opus runs of one packet too: on packet 6 one run split a fact and the other
retired one and corrected another. A sample of three packets cannot tell a smaller model's judgement from that noise;
the lost queries of Sonnet and the thinking of Haiku it does show. Lower effort saves only the wait before the first
token. The 50 s of writing stays.

**Groups that change nothing.** Of the 2,631 groups shown on 2026-10-25, 2,464 were kept; 764 were the same set of
facts shown again after the model had kept it in an earlier packet of the run. Skipping them removes up to 29 % of
the group input. It does not touch the writing of translations and queries, where the time is.

**Smaller output.** Not tried. Corrections were 88 of the run's 252 actions, and actions are a small part of an answer.

## Estimate

At 108 s a pass, the 2026-10-25 run took 191 min. Six packets per pass, in parallel, would make it about 18 passes of
about 100 s: 30–35 min at the same cost. The split by task alone would make it 106 passes of 35–90 s: 60–100 min at
1.7 times the cost. Neither is measured on a whole run yet.

Not measured: a whole `-run -all` with either change, the quality of the group decisions on packets with real
duplicates (these packets came from a base the 2026-10-25 run had already cleaned), more than one run per cell.
