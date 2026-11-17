# Repair defects the assessment finds

The merge assessment could answer `approve` or `needs-human`. On
hearthctl/lunactl#70 it reproduced a real defect: removing a Slot could delete
an Item on the wrong Page. Its only way to say so was `needs-human`, so the
pull request waited for a person to copy the finding into a comment.

The review had passed the same change. It reads the diff in separate groups of
files and may not run code; the defect lay in the interplay of three files in
three groups. The assessment looks at the whole change and runs it. The two
steps find different things, and the assessment's findings had no path to a
repair.

The assessment may now answer `needs-fix` for a concrete defect that it can
state with its trigger and consequence and that a code change can repair. The
controller hands the reason to the editor as a finding. `needs-human` stays for
what needs judgement.

Turning every `needs-human` reason into a repair was rejected: an editor
cannot repair a licensing question or decide that a risk is acceptable.

Assessment and repair may never agree, so at most two such repairs run per
pull request; then the controller reports once and waits for a person. The
assessment stays unable to change the branch itself.

The cost is that whether something is a repairable defect or a judgement call
rests on the assessment agent, and a wrong `needs-fix` costs an editor run.
