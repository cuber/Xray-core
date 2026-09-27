# AnyTLS protocol engine

Imported from SagerNet/sing-anytls commit
`7ca72921ac6aad397765670b6357fba7e67cc300` (2026-09-24).
The original GPL-3.0-or-later LICENSE applies to this directory. Retain this
notice and corresponding source with distributions; this is not MPL-only code.

Local changes expose authentication and pre-allocation stream admission hooks,
bound destination handshakes, and wait for server stream handlers on shutdown.
Closed streams cancel deadline timers and reject rearming to release retained
session state promptly, including under concurrent deadline updates.
The client engine backs Xray's native AnyTLS outbound as well as interoperability
tests. No sing-box runtime is embedded. Local hardening validates padding from
both configurations and peers, and bounds control/data write cancellation.
