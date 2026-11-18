# Search for defects once, then verify repairs

The assessment was asked for a concrete defect and answered with one. Every
assessment was also a fresh search of the whole change by an agent that knew
nothing of earlier rounds. On hearthctl/pi5-bootlab#97 that gave four rounds
of one defect each, every round costing an assessment, a repair, CI and a
review, and the limit of three repairs was reached with no end in sight: in
code like an emulator another edge case can always be found.

The first assessment of a pull request is now the one thorough search. It is
asked to keep looking after the first defect and to report every defect it
finds, numbered, and the editor repairs them in one Task. An assessment after
a repair is given the defects the newest earlier assessment found and only
verifies: are they repaired, and are the changes made since correct. It is
told not to search further.

Asking for the full list alone was rejected: a later fresh search can still
find something new, so it shortens the loop without ending it. Raising the bar
for what blocks a merge was left out: how strict the gate is stays a separate
decision.

The cost is that a defect the first assessment misses is not looked for again
by a later one. A `/reassess` renews the repair budget but not the search.
