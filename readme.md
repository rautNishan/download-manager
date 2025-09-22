## Overview

The download manager follows a simple lifecycle: create download manager → create task → calculate & persist chunks → start download (per-chunk saves and updates). The system supports pausing and resuming downloads by task ID.

This document documents the expected behavior, invariants, and the exact flow contributors should follow. A mermaid diagram follows to make the flow easy to understand.

---