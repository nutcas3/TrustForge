# TrustForge

**Core Principle: Never trust the code. Always trust the hardware boundary.**

TrustForge is a Go-based MicroVM factory for evaluating untrusted contributor verifier scripts at scale. It uses Firecracker for hardware-level isolation, snapshot-based instant-boot for throughput, vsock for zero-network host↔guest communication, and Claude for AI-powered red-team analysis.

---

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                        TrustForge Host                          │
│                                                                 │
│  ┌──────────────┐     ┌──────────────────────────────────────┐  │
│  │  REST / gRPC │────▶│            Worker Pool               │  │
│  │  cmd/api     │     │         (errgroup, N workers)        │  │
│  └──────────────┘     └──────────┬───────────────────────────┘  │
│                                  │                              │
│             ┌────────────────────┼─────────────────────────┐   │
│             │                    │                          │   │
│      ┌──────▼──────┐   ┌─────────▼──────┐   ┌───────────▶ │   │
│      │  fs.Manager │   │   vm.Factory    │   │ llm.RedTeam │   │
│      │  Task Disk  │   │  Firecracker +  │   │  (Claude)   │   │
│      │  Creation   │   │  Jailer + Snap  │   └────────────-┘   │
│      └──────┬──────┘   └────────┬────────┘                     │
│             │                   │                              │
│             │         ┌─────────▼──────────────────┐          │
│             │         │  vsock.HostClient           │          │
│             │         │  (no network, pure hw bus)  │          │
│             │         └─────────┬──────────────────┘           │
│             │                   │                              │
└─────────────┼───────────────────┼──────────────────────────────┘
              │                   │ virtio-vsock
              │         ┌─────────▼──────────────────────────────┐
              └────────▶│     Firecracker MicroVM (KVM)           │
                        │  ┌──────────────────────────────────┐  │
                        │  │  base.ext4 (ro) │ task.ext4 (rw) │  │
                        │  │  Python + libs  │ verifier.py    │  │
                        │  │                 │ output.txt     │  │
                        │  └──────────────────────────────────┘  │
                        │  ┌────────────────────────────────────┐ │
                        │  │     guest_agent (Go binary)        │ │
                        │  │  mounts /dev/vdb, runs verifier,   │ │
                        │  │  streams result back via vsock     │ │
                        │  └────────────────────────────────────┘ │
                        └─────────────────────────────────────────┘
```

---

## Directory Structure

```
trustforge/
├── cmd/
│   ├── api/              # REST + gRPC API server entrypoint
│   └── guest_agent/      # Guest binary (runs inside the VM)
├── internal/
│   ├── fs/               # Dynamic ext4 task disk creation
│   ├── vm/               # Firecracker MicroVM factory + snapshots
│   ├── vsock/            # Host↔Guest vsock communication
│   ├── llm/              # Claude-based red-team analysis
│   ├── scoring/          # Trust decision engine
│   └── worker/           # Concurrent VM lifecycle pool
├── pkg/
│   ├── models/           # Core domain types
│   └── config/           # Configuration management
├── scripts/
│   └── build_base_image.sh  # Builds the Alpine base.ext4
├── config.yaml           # Default configuration
├── Dockerfile
└── Makefile
```

---

## The "Instant-Boot" Strategy

Cold-booting a Firecracker VM takes ~125ms. At millions of evaluations, that's unacceptable.

**Solution: Memory Snapshots**

1. **Warm-Up** (once at startup): Boot a VM with the base image. Wait for the guest agent to signal Python is ready. Take a full memory snapshot (`vm.WarmUpSnapshot`).
2. **Resume** (per evaluation): `vm.ResumeFromSnapshot` restores the full VM state from the snapshot file in **<5ms**. The unique task disk is injected as `/dev/vdb` at resume time.
3. **Replenishment**: A background goroutine (`worker.replenishSnapshots`) continuously refills the warm pool as snapshots are consumed.

---

## The Evaluation Pipeline

For every submission, the worker runs this state machine:

```
PENDING → SANDBOXING → RUNNING → RED_TEAM → TRUSTED / REJECTED
```

| Stage | Component | Action |
|---|---|---|
| Ingestion | `cmd/api` | Accepts verifier + model output via REST POST |
| Sandbox Prep | `internal/fs` | Creates 10MB task.ext4 with verifier.py + output.txt |
| Execution | `internal/vm` | Resumes Firecracker from warm snapshot (<5ms) |
| Vsock Comms | `internal/vsock` | Sends `RUN` command; receives `EvaluationResult` JSON |
| Red-Team | `internal/llm` | Claude analyzes for reward hacking patterns |
| Scoring | `internal/scoring` | All gates must pass to reach TRUSTED status |

---

## Security: The Jailer

Every VM is wrapped in a Firecracker Jailer:

- **Namespacing** — VM process cannot see the host's PID/net namespaces
- **Chroot** — VM process can only see its own disk files, nothing else on the host
- **Cgroups** — Hard limits on CPU and RAM prevent noisy-neighbor attacks
- **UID/GID isolation** — VM runs as an unprivileged user (uid 1001)

---

## Vsock Communication

There is **no network interface** in the VM. Communication happens entirely through the virtio-vsock device — a hardware bus that connects host CID 2 to guest CID 3.

- **Port 52**: Host sends `{"type":"RUN","submission_id":"..."}`, guest replies with `EvaluationResult` JSON
- **Port 53**: Guest sends `READY\n` once Python is initialized (triggers snapshot during warm-up)

This means there is no TCP/IP stack, no firewall rules, no IP allocation. The attack surface is minimal.

---

## Red-Team Analysis

Before any submission reaches `TRUSTED`, the verifier code is analyzed by Claude for:

- Hardcoded score values
- File system snooping (reading expected outputs)
- Environment fingerprinting (test vs. production detection)
- Non-deterministic scoring
- Reward inflation without genuine evaluation

Submissions with a risk score ≥ 0.70 are automatically `REJECTED`.

---

## Quick Start

### Prerequisites

- Linux with KVM enabled (`/dev/kvm`)
- Firecracker + Jailer binaries in PATH
- Docker (for building the base image)
- Go 1.24+

### Setup

```bash
# 1. Build everything
make build

# 2. Build the Alpine base.ext4 image (requires root for loop mount)
sudo make base-image

# 3. Configure
cp config.yaml /etc/trustforge/config.yaml
# Edit API key, paths, etc.

# 4. Run
ANTHROPIC_API_KEY=sk-ant-... make run
```

### Submit a Verifier

```bash
curl -X POST http://localhost:8080/v1/submissions \
  -H "Content-Type: application/json" \
  -d '{
    "contributor_id": "alice",
    "verifier_code": "import sys\n\ndef verify(output):\n    score = float(len(output.strip()) > 10)\n    print(f\"SCORE: {score}\")\n\nwith open(\"/task/output.txt\") as f:\n    verify(f.read())\n",
    "model_output": "The answer is 42."
  }'
```

### Health Check

```bash
curl http://localhost:8080/v1/health
# {"active_vms":3,"failed_jobs":0,"queue_depth":12,"status":"ok","total_jobs":1847,"warm_snaps":8}
```

---

## Configuration Reference

| Key | Default | Description |
|---|---|---|
| `worker.pool_size` | 50 | Max concurrent VMs |
| `worker.warm_snapshot_count` | 10 | Warm snapshot pool size |
| `firecracker.mem_size_mib` | 128 | RAM per VM |
| `firecracker.execution_timeout` | 30s | Max wall-clock time per eval |
| `llm.risk_threshold` | 0.70 | Reject above this red-team score |
| `storage.task_disk_size` | 10MB | Ephemeral task disk size |

---

## Performance Targets

| Metric | Target |
|---|---|
| VM resume time | < 5ms |
| Cold boot time | ~125ms |
| Max concurrent VMs | 50 (configurable) |
| Evaluations/sec (warm pool) | ~200 |
| Task disk creation | < 100ms |
