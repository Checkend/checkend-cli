# Security policy

## Report a vulnerability

Do not open a public issue for a security problem.

Report it privately on GitHub: open the
[Security tab](https://github.com/Checkend/checkend-cli/security) and click **Report a vulnerability**.
Include the steps to reproduce, the affected version (`ce --version` shows it), and the impact.

We confirm each report within 3 working days. We tell you when the fix is released, and we
credit you in the release notes if you want.

## Supported versions

Checkend is in early development. Only the latest release and `main` get security fixes.

## Check a release

Each release has `checksums.txt` (the SHA-256 of each file) and `checksums.txt.sig`, an SSH
signature of it made with a Checkend release key (ed25519, namespace `checkend-cli-release`).
The keys are the allowed signers in [install.sh](install.sh). To check a download by hand:

```sh
sed -n '/^# BEGIN ALLOWED SIGNERS$/,/^# END ALLOWED SIGNERS$/p' install.sh > allowed_signers
ssh-keygen -Y verify -f allowed_signers -I <key id> -n checkend-cli-release \
  -s checksums.txt.sig < checksums.txt
shasum -a 256 -c checksums.txt --ignore-missing
```

`<key id>` is the first word of a key line in `allowed_signers`. The server releases are signed
with another key, in the namespace `checkend-server-release`.

## Scope

In scope: the code in this repository (the `ce` command-line interface).

Out of scope: vulnerabilities in the Checkend server (report them in Checkend/checkend), in Node.js, or in your own settings.
Report those to their owners.
