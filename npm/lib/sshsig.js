// Verifies an SSH signature (the SSHSIG format of `ssh-keygen -Y sign`, PROTOCOL.sshsig of
// OpenSSH) made with an ed25519 key. The CLI releases sign checksums.txt this way.
import { createHash, createPublicKey, verify } from "node:crypto";

export const NAMESPACE = "checkend-cli-release";

const notSSH = () => new Error("checksums.txt.sig is not an SSH signature.");

/** Reads the SSH strings of buf; throws when a length is wrong or bytes are left over. */
function strings(buf, count) {
	const out = [];
	let offset = 0;
	for (let i = 0; i < count; i++) {
		if (offset + 4 > buf.length) throw notSSH();
		const length = buf.readUInt32BE(offset);
		if (offset + 4 + length > buf.length) throw notSSH();
		out.push(buf.subarray(offset + 4, offset + 4 + length));
		offset += 4 + length;
	}
	if (offset !== buf.length) throw notSSH();
	return out;
}

function sshString(buf) {
	const length = Buffer.alloc(4);
	length.writeUInt32BE(buf.length);
	return Buffer.concat([length, buf]);
}

/**
 * Checks the armored signature of message for namespace. The key of the signature must be one of
 * allowed ([{ id, key: "ssh-ed25519 AAAA…" }]). Returns the id of the key; throws otherwise.
 */
export function verifySignature(armored, message, namespace, allowed) {
	const text = String(armored).trim();
	const begin = "-----BEGIN SSH SIGNATURE-----";
	const end = "-----END SSH SIGNATURE-----";
	if (!text.startsWith(begin) || !text.endsWith(end)) throw notSSH();
	const base64 = text.slice(begin.length, -end.length).replace(/\s+/g, "");
	if (!/^[A-Za-z0-9+/]+=*$/.test(base64)) throw notSSH();
	const blob = Buffer.from(base64, "base64");
	if (blob.length < 10 || blob.subarray(0, 6).toString() !== "SSHSIG" || blob.readUInt32BE(6) !== 1) throw notSSH();
	const [publicKey, ns, , hash, signature] = strings(blob.subarray(10), 5);
	if (ns.toString() !== namespace) throw new Error(`checksums.txt.sig is for namespace "${ns}", not "${namespace}".`);
	const [keyType, raw] = strings(publicKey, 2);
	if (keyType.toString() !== "ssh-ed25519" || raw.length !== 32) throw new Error("checksums.txt.sig is not made with an ed25519 key.");
	const match = allowed.find(({ key }) => {
		const [type, data] = String(key).trim().split(/\s+/);
		return type === "ssh-ed25519" && Buffer.from(data ?? "", "base64").equals(publicKey);
	});
	if (!match) {
		const fingerprint = createHash("sha256").update(publicKey).digest("base64").replace(/=+$/, "");
		throw new Error(`checksums.txt.sig is made with an unknown key SHA256:${fingerprint}.`);
	}
	const algorithm = { sha512: "sha512", sha256: "sha256" }[hash.toString()];
	if (!algorithm) throw new Error(`checksums.txt.sig uses an unknown hash "${hash}".`);
	const digest = createHash(algorithm).update(message).digest();
	const signed = Buffer.concat([Buffer.from("SSHSIG"), sshString(Buffer.from(namespace)), sshString(Buffer.alloc(0)), sshString(hash), sshString(digest)]);
	const [sigType, sig] = strings(signature, 2);
	const key = createPublicKey({ key: { kty: "OKP", crv: "Ed25519", x: raw.toString("base64url") }, format: "jwk" });
	if (sigType.toString() !== "ssh-ed25519" || !verify(null, signed, key, sig)) {
		throw new Error("The signature of checksums.txt is not valid.");
	}
	return match.id;
}
