---
name: memcue-dream
description: Tidy long-term memory (memcue dream). Use when the session start says "facts wait for the dream" and the user's task allows a pause, or when the user asks to tidy, clean up or deduplicate memory ("приведи память в порядок", "dream").
---

# memcue: the dream

A dream splits compound facts, gives facts without search queries their queries, translates facts into the other
language, and retires or corrects duplicates and outdated facts. You do it; memcue prepares the work and checks your
answer.

Never interrupt the user's work for it: finish or pause at a natural point first.

A due dream usually needs no session: with `claude` on `PATH` or `MEMCUE_API_URL` set, the session start runs it in
the background and says nothing (#569). The session start names the dream only when nothing can run it there.

1. Print the work:

   ```bash
   memcue dream -prepare
   ```

   It prints the instructions, the four tasks with their rules and the data. "nothing for the dream" means you are
   done.
2. Follow the instructions: write one JSON answer to a temporary file (`mktemp`), with `packet` exactly as printed.
3. Apply it:

   ```bash
   memcue dream -apply "$ANSWER"
   ```

   It prints what it changed and what it refused, with the reason. An answer to an older packet is refused whole:
   run `-prepare` again.
4. Tell the user in one or two lines what changed: facts split, given queries, translated, retired,
   corrected.

A packet is bounded; when the session start still says facts wait, the next packet takes the rest.

A base far behind (hundreds of facts waiting, or a translation owed for most of them) takes dozens of packets, about
25,000 tokens of context each. Do not answer them in the session: when the user wants the whole base done, run it in
the background on `claude -p` and report the result.

```bash
env -u MEMCUE_API_URL -u MEMCUE_MODEL memcue dream -run -all
```

It answers packet after packet until the base has no work left, and logs each pass with its cost in `dream.log`.
Without `env -u` it uses the endpoint of `MEMCUE_API_URL` when that is set.
