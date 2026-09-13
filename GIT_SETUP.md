# Push this snapshot to Git

After extracting the repository ZIP:

```bash
cd netbird-audit
git init
git add .
git commit -m "Initial NetBird Audit development snapshot"
git branch -M main
git remote add origin <YOUR_GIT_REMOTE_URL>
git push -u origin main
```

Before the first push:

```bash
make check
```

Review `docs/HANDOFF.md` and confirm no deployment secrets/runtime files were copied into the directory.
