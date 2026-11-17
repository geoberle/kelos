# Let the assessment judge binary files

OCR does not review a binary file and lists it as excluded with the reason
`binary`. The review consumer required every changed file to be reviewed and
accepted only deleted files as excluded, so a pull request that added a font
never reached `review-done` and waited for a person.

A code review cannot say anything about a binary file, so requiring one adds
no safety. The consumer now accepts a `binary` exclusion when GitHub has no
text diff for the same file. The merge assessment is told which files the
review could not read and must establish for each what it is, where it comes
from, whether its content is verified against that source and whether anything
executes it. A file whose origin or purpose it cannot establish means
`needs-human`.

Accepting every exclusion was rejected: OCR also excludes secrets and
oversized files, and those must still stop for a person. Accepting binary
files without telling the assessment was rejected because binary does not mean
harmless; a compiled program would pass like a font.

The cost is that the judgement on a binary file rests on the assessment agent
alone.
