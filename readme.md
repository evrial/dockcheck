Fastest and simplest Docker image update checker.
No dependencies, only native Docker API calls.
Doing only one thing, and doing it well.

Building:
```
go mod tidy
go build -o dockcheck
```

Usage of ./dockcheck:
```
  -a    Automatic updates without interaction
  -e string
        Comma-separated list of container names to exclude
  -n    Check availability only
  -p    Auto-prune dangling images after update
  -s    Include stopped containers
  -t int
        Timeout in seconds per container lookup (default 10)
  -x int
        Max concurrent asynchronous lookups (default 10)
```

```
dockcheck caddy
Checking 1 containers for updates...

Containers on latest version:
  • caddy

No updates available.
```

# Podman

Podman provides a **Docker-compatible API socket**, which means the Go Docker SDK can talk directly to Podman's engine.

---

### How to run it with Podman

#### 1. Enable the Podman Socket

Unlike Docker, Podman doesn't run a background daemon by default. You need to enable its API socket service:

**For rootless (user-level) Podman:**

```bash
systemctl --user enable --now podman.socket

```

**For system-wide (root) Podman:**

```bash
sudo systemctl enable --now podman.socket

```

---

#### 2. Direct `dockcheck` to the Podman Socket

The Go Docker SDK looks for the socket specified in the `DOCKER_HOST` environment variable. Point it to your Podman socket when executing the binary:

**Rootless Podman:**

```bash
DOCKER_HOST="unix://$XDG_RUNTIME_DIR/podman/podman.sock" ./dockcheck

```

**Root Podman:**

```bash
DOCKER_HOST="unix:///run/podman/podman.sock" ./dockcheck

```

---

### Pro-Tip: Make it permanent (Alias)

If you use Podman exclusively on your machine, you can add an export or alias to your shell profile (`~/.bashrc` or `~/.zshrc`):

```bash
export DOCKER_HOST="unix://$XDG_RUNTIME_DIR/podman/podman.sock"

```

Once that variable is set in your environment, running `./dockcheck` will automatically interact with Podman just like it would with Docker!