#!/bin/sh
# The whole #531 run on the docker host, unattended: pipeline.sh, from ROOT (~/stand531) with run.py, extract.py,
# grade.py, grade_prompt.md and disagree.py beside it. It waits for a grid already running in the tmux session
# stand531, runs the grid again (finished runs are skipped, unfinished ones run anew, chains after their captures),
# extracts the records once, grades them in passes A and B, and grades again in pass C where A and B disagree. Every
# step appends to pipeline.log; nothing is deleted.
set -u
cd "$(dirname -- "$0")"
log=pipeline.log
say() { echo "$(date '+%F %T') $*" >> "$log"; }
say "waiting for the first grid"
while ! grep -q '^grid-exit=' grid.log 2>/dev/null; do sleep 30; done
say "grid again"
python3 -I run.py grid --repeats 3 --jobs 4 --budget 25 >> grid2.log 2>&1
say "grid again exit $?: $(python3 -I run.py cost)"
if [ -e records ]; then say "records exist, extract skipped"; else python3 -I extract.py runs records >> "$log" 2>&1; fi
for p in A B; do
	say "grading pass $p"
	python3 -I grade.py records fixtures/labels.json grades --pass "$p" --jobs 4 > "grade-$p.log" 2>&1
	say "pass $p exit $?"
done
only=$(python3 -I disagree.py grades 2>> "$log")
if [ -n "$only" ]; then
	say "grading pass C on $(echo "$only" | tr ',' '\n' | wc -l) records"
	python3 -I grade.py records fixtures/labels.json grades --pass C --jobs 4 --only "$only" > grade-C.log 2>&1
	say "pass C exit $?"
fi
say "done"
