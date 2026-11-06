# Licensing DSH Work

Use all DSH Work functionality under AGPL-3.0-only, with separately licensed
components retaining their terms. Uses requiring an exception need express
[written permission](../COMMERCIAL.md) at the rights holders' discretion.
The repository's [LICENSE](../LICENSE) contains the unmodified AGPLv3 text.
The version choice
is version 3 only, not an automatic grant under future license versions.

## What the project license covers

The default applies to project-owned source, documentation and assets unless
a file or component states different terms. Existing third-party copyright,
license and attribution notices remain in effect. In particular,
`browser-extension/LICENSE`, `browser-extension/NOTICE` and
`browser-extension/PROVENANCE.txt` describe the Apache-2.0 browser extension
component and its upstream provenance; that component keeps its stated license.
Dependencies, separately managed DSH runtimes and plugins retain their own terms.

The project license does not grant trademark rights. Forks should use distinct
branding so users can distinguish them from official releases. Accurate
attribution and references to compatibility or origin remain appropriate.
See [Name and brand use](branding.md) for naming, assets and distributions.

## Using and distributing the software

Commercial activity is permitted under the AGPL. When conveying covered
software, follow its corresponding-source, notice and license requirements.
When a modified version supports remote network interaction, section 13
requires an offer of its Corresponding Source to the users interacting with it
remotely. The precise requirements and exceptions are in the license itself.

Separate licensing is a reserved exception, not a commercial product tier or a
promise to sell licenses. Merely using an AI tool to transform
covered code does not itself grant an exception from the applicable license.

## Contributions and commercial rights

Before merging an external copyrightable contribution, the maintainer must
obtain a separately executed contributor agreement granting the rights needed
to distribute that contribution under both AGPL-3.0-only and commercial terms.
Contributors retain copyright unless their signed agreement says otherwise.
A pull request, a sign-off or an AGPL-only grant alone is not permission for
proprietary relicensing. Until that agreement is executed and retained, keep
the contribution unmerged or use an independently authored replacement.

Before granting an exception, verify the rights for every included
contribution and identify third-party obligations and exclusions in the
agreement. Git authorship is evidence of provenance, not proof of ownership
or authority from an employer. The authorized licensor and contributor agreement
must be settled before accepting external code or granting exceptions.

## Rights and enforcement

[COMMERCIAL.md](../COMMERCIAL.md#copyright-and-legal-remedies) states the
reservation of ungranted rights and available legal remedies without adding
restrictions to the AGPL. Use the [enforcement guide](enforcement.md) to establish
ownership, preserve evidence and assess a suspected violation before sending
notices. A competing product, payment, a fork or AI use alone is not evidence
of infringement. The basis must identify covered material and a specific
unfulfilled obligation or other actionable right.

## Release checklist

- Include LICENSE and COMMERCIAL.md with distributed application packages.
- Provide the exact Corresponding Source and build/install information required
  by the AGPL for the released binaries; keep the source offer accessible to
  recipients. A repository URL alone does not establish completeness.
- Preserve third-party license texts and notices for bundled dependencies.
- Keep separately licensed components identified in source and distributions.
- Check contribution rights before issuing a commercial grant.
- Retain the release's license scope, exact source revision and source archive,
  build inputs and notices with the release record. Record separately licensed
  paths and the permission supporting any exception.

The Windows installer includes LICENSE and COMMERCIAL.md beside the application.
Its inclusion of these documents does not replace the source and third-party
compliance steps above. Other binary distribution paths must include them too.

## Why this licensing model

AGPL plus discretionary written exceptions permits open collaboration
while requiring reciprocal source availability for covered derivatives and
modified network services. Apache-2.0 was rejected as the project default
because it permits closed-source derivatives. A noncommercial or
no-competition license was rejected to retain an open-source license that
permits commercial use. Revisit this choice if contribution rights cannot be
secured or adoption requirements materially change.

Previously granted licenses for older versions and separately licensed
components remain effective under their terms; this default does not revoke
those grants.
