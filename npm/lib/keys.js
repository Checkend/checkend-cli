// The keys that sign CLI releases: { id, key: "ssh-ed25519 AAAA…" }, the same keys as the allowed
// signers of install.sh. A new key is added here before its first release; an old key stays
// until no supported release uses it. Empty: no key exists yet, so the wrapper installs nothing.
export const ALLOWED_SIGNERS = [];
