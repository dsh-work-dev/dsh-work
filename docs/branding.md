# DSH Work name and brand use

**DSH Work** is the product name in every language. Use this spelling in
documentation prose, licensing materials, window titles, menus, notifications,
installer display names and product metadata. Keep the space and capitalization.

DSH Work is a desktop host for DeepSeek Harness (DSH). Describe that relationship
as compatibility and integration, not as an assertion of upstream sponsorship
or endorsement. The product name is not a legal entity or a substitute for the
actual rights holder's identity in a contract or formal claim.

## Product name versus technical identifiers

| Context | Form |
| --- | --- |
| Product, public title and prose | DSH Work |
| Development notification display name | DSH Work Dev |
| Repository | `dsh-work-dev/dsh-work` |
| Executable and CLI | `dsh-work.exe`, `dsh-work-cli.exe` (without `.exe` on other platforms) |
| Go module and plugin scope | `github.com/local/dsh-work`, `@dsh-work/*` |
| Protocol and application identifier | `dsh-work://`, `com.local.dsh-work` |
| Managed profile and storage keys | Existing `dsh-work` identifiers |

Keep filenames, commands, code examples, URLs, data paths, registry keys, package
names and IPC identifiers exact. Build configuration fields such as
`info.productName` and `info.companyName` also feed installation and registry
identity; their technical values are not display-name templates to rename.
Windows metadata, NSIS display names, macOS display names and Linux desktop
names already use DSH Work independently of those identifiers.

The distinction keeps a consistent public name without changing existing
installations or user data. Revisit technical identifiers only as a separately
designed compatibility migration.

## Existing visual assets

The current mark is the whale-tail silhouette. Use the repository's supplied
assets rather than redrawing, stretching, rotating or adding effects to them:

| Use | Repository asset |
| --- | --- |
| Application icon | `frontend/public/branding/dsh-work-appicon.png` |
| Light-background application icon | `frontend/public/branding/dsh-work-appicon-light.png` |
| Standalone mark variants | `frontend/public/branding/dsh-work-mark.png`, `dsh-work-mark-black.png` in the same directory |
| Tray variants | `frontend/public/branding/dsh-work-tray.png`, `dsh-work-tray-dark.png`, `dsh-work-tray-template.png` |
| Agent pointer geometry | `frontend/public/branding/agent-pointer.json` and its generated `agent-pointer.svg` |

Keep the mark's aspect ratio and sufficient contrast against its background.
Do not add an enclosing shape or crop the silhouette. The pointer is an
interaction asset, not a replacement application logo. Keep brand identity
consistent across application, extension and documentation.

## References, redistribution and forks

Accurate text references, reviews, tutorials and compatibility statements may
identify DSH Work without implying endorsement. Screenshots may show the
original interface when describing it, subject to applicable rights and law.
For example: “Example Client is based on DSH Work; independently maintained
and not affiliated with the DSH Work project.”

Use distinct product names, icons and publisher identity for modified or
independently maintained products. Make the modification and upstream origin
clear, while preserving required copyright, attribution and license notices.
Do not describe a fork as an official release, an authorized edition or a
partner product without the relevant written permission. Retaining upstream
attribution does not require using upstream branding as the fork's identity.

When redistributing an unmodified official package, identify its version and
original source and accurately identify yourself as the distributor. Do not
imply that your download site, support or additional services are official.
Code-signing and checksums should be assessed against the original release's
actual verification information, not an assumed publisher identity.

The [source repository](https://github.com/dsh-work-dev/dsh-work) and its
[Releases](https://github.com/dsh-work-dev/dsh-work/releases) are the project
reference channels. Treat other publishers' packages as third-party
distributions unless the project explicitly identifies them as official.

## Code licensing and brand permission are separate

The [project license](../LICENSE) governs copyright permissions, including
covered artwork unless a component states otherwise. This policy does not
remove those permissions or add a restriction to the AGPL. Copyright permission
to copy an image is distinct from permission to use it as a confusing product
identity. Any trademark claim depends on actual rights and applicable law.

No trademark license or endorsement is granted by the code license or by a
code-licensing exception alone. Requests to use the name or mark as branding
should identify the proposed product, artwork, placement and claimed
relationship through the [licensing inquiry channel](../COMMERCIAL.md#request-an-exception).
Any express permission must identify the relevant rights holder, permitted use
and scope. Preserve lawful attribution and references to origin.

Use a registration symbol only when the rights holder has verified the relevant
registration and its scope. Before a formal brand complaint, establish the
claimant's identity and rights and preserve evidence of the misleading use;
follow [the enforcement guide](enforcement.md).
