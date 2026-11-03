# Coding standards

Read during review. Mechanical rules (formatting, unused code, direct Discord sends, show-name matching) are enforced by checks, not listed here.

## Behaviour parity

A change described as behaviour-preserving keeps every check the old code ran on each call running on each call.

- A cache, memo or hoisted lookup replays the preconditions of the uncached path before it returns an entry. Permission, state and ownership checks are preconditions.
- For each deleted or moved call, name where its guard now runs. A guard with no new home is a finding.
- Moving a call into a callee moves it relative to the callee's guards. List the checks that ran after the call at the old site; each still runs after it at the new one. Trace a late-ending path too: a wait that ends in success after shutdown must still reach the shutdown check before any send.
- The spec and its tickets can disagree with each other ("resolve on a miss" beside "behaviour unchanged"). Review against the behaviour; report the disagreement.

## Glossary vocabulary

Names, comments and test titles use the terms in `GLOSSARY.md`. A term listed under its `_Avoid_` line is a finding, as is a coined synonym for a defined term.
