# Responding to suspected infringement

Build a claim from an identifiable rights holder, protected material, an
applicable license and a documented violation. The public reservation of rights
is in [COMMERCIAL.md](../COMMERCIAL.md); [licensing](licensing.md) defines scope.

## Establish the right to act

Before a formal demand or platform complaint, the project owner must privately
record the claimant's legal identity and contact details, the works they own,
and any authorization to act for other owners. Retain original development
records and any employer releases, assignments or contributor agreements.
An account name, repository ownership or Git author field alone does not
establish ownership of every file or authority to enforce another person's work.

For each disputed file, identify original material, upstream material, its
license and the version used. The Apache-2.0 browser extension and dependencies
must be assessed under their own terms. Prior grants for older versions also
matter. Do not claim exclusive rights in common upstream code. Preserve the
license declarations that accompanied each relevant version.

Ask counsel in the relevant jurisdiction whether registration, evidence
preservation formalities, limitation periods or other prerequisites affect the
planned remedy. Record actual registrations and enforcement authorizations
with their supporting documents before relying on them in a claim.

## Keep release evidence before a dispute

Keep recoverable repository history and original work records. For each public
release retain its source revision and source archive, license and notices,
build inputs and instructions, binaries, publication location and publication
record. Store signed agreements and identity documents in access-controlled
private storage. Git timestamps and signed tags help establish provenance but
are not, on their own, independent proof of authorship or a trusted timestamp.

Document the origin and human creative contributions for AI-assisted material
where relevant. Preserve design drafts and substantive revisions rather than
assuming every generated output supports the same copyright claim. Have counsel
assess protection where authorship or originality is disputed.

## Preserve a suspected violation

Create a private case record; do not commit evidence bundles or personal details
to the public repository. Record:

- The observer, collection date and timezone, original URLs, repository and
  revision, product version, publisher and distribution channel.
- Lawfully obtained original source archives, downloadable packages, pages and
  screenshots showing distribution, notices, source offers or branding.
- The exact steps used to find or request Corresponding Source and the response,
  including dates. Failure to find a link alone may not prove noncompliance.
- A file/range comparison connecting our protected material to the suspected
  copy, separating common dependencies, generated boilerplate and functional
  similarities. Preserve raw originals separately from annotations.
- The applicable license and precise requirement at issue, plus correspondence
  and any permission or exception the other party claims.

Collect only through authorized access. If execution is necessary, inspect
untrusted packages in an isolated environment. For anticipated litigation,
arrange evidence preservation with counsel before the material disappears.

## Match the allegation to the facts

| Observation | Assessment needed |
| --- | --- |
| Covered binaries distributed without adequate source access | Check AGPL section 6, distribution method and the actual Corresponding Source offered. |
| Covered modified source distributed without required notices or licensing | Check AGPL sections 4 and 5 and identify the omitted obligation. |
| Modified covered software supports remote network interaction | Check AGPL section 13 and the source offer available to interacting users. |
| A product incorporates covered code into a proprietary combined work | Assess the work's boundaries and applicable license with counsel; mere coexistence or communication alone does not settle coverage. |
| Apache-licensed component reused | Check Apache conditions; closed-source reuse alone is not a violation of that license. |
| Official branding or endorsement appears to be copied | Assess actual trademark, artwork or misleading-origin rights separately from AGPL compliance. |
| Similar functions or allegedly AI-rewritten code | Identify protected copied expression and provenance; feature similarity or an AI detector score alone does not establish infringement. |

Copyright protects expression rather than ideas or methods; see the
[US Copyright Office overview](https://www.copyright.gov/help/faq/faq-protect.html).
The controlling law and facts must be assessed for the actual claim.

## Seek compliance, then escalate where justified

After preserving evidence, an authorized rights holder or representative can
send a factual compliance notice. Identify the work and ownership, the accused
version and URLs, matched material, applicable clauses and the specific remedy
sought. Request an explanation or compliance response by an explicit reasonable
date. A requested response date is not a substitute for statutory or license
deadlines. Retain delivery and response records.

For AGPL violations, assess termination and reinstatement under section 8 of
[LICENSE](../LICENSE). It includes provisional and permanent reinstatement
conditions, including the 60-day notice condition and the first-notice 30-day
cure provision. Ceasing a violation does not by itself resolve every potential
past claim, and this notice cannot override reinstatement granted by LICENSE.

If unresolved, counsel and the rights holder should choose the appropriate
platform complaint, further demand, settlement or court remedy. Any requested
injunction, damages or costs must have a factual and legal basis; the repository
creates no fixed penalty. Urgent preservation or court measures may require
acting without waiting for a voluntary response.

For GitHub, read the current
[DMCA notice guide](https://docs.github.com/en/site-policy/content-removal-policies/guide-to-submitting-a-dmca-takedown-notice)
and [takedown policy](https://docs.github.com/en/site-policy/content-removal-policies/dmca-takedown-policy)
before filing. Submit only an investigated, truthful copyright claim with
specific targets and the required declarations. Trademark complaints use a
different process. Notices may be published; counter-notices can trigger
restoration unless the required legal action is taken. Confirm current deadlines
with counsel and the platform at the time of action.

Public statements should identify verified facts and distinguish allegations
from findings. Record the requested correction and its outcome. When a case
closes, preserve the evidence and resolution privately, and correct or retract
unsupported complaints through the relevant channel.
