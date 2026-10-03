Vendored global-hotkey 0.8.0 (MIT/Apache-2.0).

Mac-only `register_exclusive` opts one registration into Carbon exclusivity and enabled-system-shortcut preflight. `register` retains upstream shared ownership. Native errors include OSStatus. Only the Mac backend is retained; attn gates this dependency to Mac. macOS rejects exclusive incumbents but cannot detect existing shared owners; this is not universal conflict discovery.
