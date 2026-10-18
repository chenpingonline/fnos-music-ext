# fnMusic Go 0.5.0 x86 FPK installation rejection

Reported error: 飞牛应用中心：不是有效的 fpk 文件。

## Confirmed problems in the previous hand-assembled artifact

- `config/resource` was absent; the official minimum project structure requires it.
- The outer Python PAX tar had extended metadata. Whether PAX caused the rejection is not independently verified.
- The previous artifact was not produced by official `fnpack`; archive extraction alone cannot establish fnOS installability.

## Preferred build

Use the official fnpack version 1.2.3+ on Linux with Go 1.23+:

```bash
bash scripts/build-fpk-x86.sh
```

This compiles only Linux AMD64 and creates a clean staging directory with `app/`, `cmd/`, `config/{privilege,resource}`, `wizard/`, `manifest`, and icons, then calls `fnpack build`. Do not package the ARM64 binary in the x86 FPK.

When official fnpack is not available, a compatibility GNU tar-built candidate was generated separately for local testing. It has been audited for MD5, ELF architecture, paths, and tar headers, but has **not** passed a real fnOS installer test.

Before production use, verify manual installation, start/stop, socket recovery, upgrade and uninstall in a dedicated fnOS test machine. If fnOS rejects the package, collect the output of `sudo appcenter-cli install-fpk /path/to/file.fpk` and the fnOS version.
