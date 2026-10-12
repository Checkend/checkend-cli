// The keys that sign CLI releases: { id, key: "ssh-ed25519 AAAA…" }, the same keys as the allowed
// signers of install.sh. A new key is added here before its first release; an old key stays
// until no supported release uses it. A test pins each key to its fingerprint.
export const ALLOWED_SIGNERS = [
	// SHA256:yoR4iXfhZdJ4v5OaLXX+5/rupH9z93AhZGdas6/AP88
	{ id: "cli-2026", key: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAION9vmE2Vp0SRkz+rdN0fnC9IvpPP5chwaCbNOLXlEI/" },
];
