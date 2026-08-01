Fastest and simplest Docker image update checker.
No dependencies, only native Docker API calls.
No rate limits for checking, only remote.Head method.
Native operation with remote Docker nodes using TCP and SSH connection.
Podman and Hawser (Dockhand agent) support.
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
dockcheck -a -p
Checking 2 containers for updates...

CONTAINER            IMAGE                               STATUS               LOOKUP TIME
---------            -----                               ------               -----------
homeassistant        ghcr.io/home-assistant/home-as...   up-to-date           533ms
caddy                caddy:latest                        up-to-date           903ms

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


# Remote connection

The best part is **native Go code already supports TCP Docker sockets out of the box** without changing a single line of logic!

Because we initialized the client using:

```go
cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())

```

The standard `docker/client` SDK automatically respects the `DOCKER_HOST` environment variable.

---

### Why It’s a Great Idea

1. **Remote Operations & Centralized Management:** You can run this tool on a lightweight administrative machine (or your local laptop) to check and trigger updates across multiple distant Docker nodes, remote VMs, or home servers.
2. **Containerized Execution:** If you run this tool inside a container itself, pointing to a TCP socket (or a socket proxy) avoids having to mount `/var/run/docker.sock` directly into the container filesystem.

---

### Security Considerations (Crucial)

Exposing a raw Docker TCP socket directly to a network can be risky, as anyone with access to the port gets root-equivalent access to the host system. If you choose to go the TCP route, follow these best practices:

#### 1. Avoid Unencrypted HTTP TCP (`tcp://0.0.0.0:2375`)

Never expose port `2375` publicly or across untrusted networks without protection.

#### 2. Option A: Use TLS (Recommended for Direct TCP)

Secure the daemon over port `2376` using mutual TLS certificates. Your Go app will automatically respect it if you export:

```bash
export DOCKER_HOST="tcp://192.168.1.100:2376"
export DOCKER_TLS_VERIFY="1"
export DOCKER_CERT_PATH="/path/to/certs"

```

#### 3. Option B: Use SSH Tunnels (Simplest & Most Secure)

Instead of modifying daemon settings, you can route the Docker client over SSH! Docker’s Go SDK supports SSH URLs natively:

```bash
export DOCKER_HOST="ssh://user@remote-docker-host"
./dockcheck

```

#### 4. Option C: Use a Read-Only Docker Socket Proxy

If you only plan to run checks (e.g., using the `-n` dry-run flag) without triggering container recreations, point `DOCKER_HOST` to a Docker Socket Proxy (like `tecnativa/docker-socket-proxy`) where `POST`/`DELETE` calls are disabled and only `GET` requests are allowed.

---

### How to Use It Right Now

To target a remote daemon running over TCP (or SSH), just set the environment variable before running your compiled binary:

```bash
# TCP with TLS
DOCKER_HOST="tcp://192.168.1.50:2376" DOCKER_TLS_VERIFY=1 DOCKER_CERT_PATH=~/.docker/certs ./dockcheck

# Or via SSH
DOCKER_HOST="ssh://root@192.168.1.50" ./dockcheck

```

# Remote connection to Hawser agent

Hawser is a lightweight proxy/agent designed to expose the Docker API remotely, and in its standard mode, it uses token authentication with optional TLS certificates.

```bash
export DOCKER_HOST="tcp://100.97.241.8:2376"
export HAWSER_TOKEN="your_actual_hawser_token_here"

./dockcheck
```