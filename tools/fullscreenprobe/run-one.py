#!/usr/bin/env python3
"""Bound a diagnostic process and clean up only its newly created process group."""
import os
import signal
import subprocess
import sys

child = subprocess.Popen(sys.argv[1:], start_new_session=True)
try:
    result = child.wait(timeout=170)  # outside the unchanged 150s Go test limit
except subprocess.TimeoutExpired:
    print(f"probe supervisor: process {child.pid} exceeded 170s", flush=True)
    result = 124
finally:
    # A timed-out Go test parent can leave its AppKit child alive. This group
    # was created solely for this attempt, never for another job's processes.
    try:
        os.killpg(child.pid, signal.SIGTERM)
    except ProcessLookupError:
        pass
    try:
        child.wait(timeout=2)
    except subprocess.TimeoutExpired:
        os.killpg(child.pid, signal.SIGKILL)
        child.wait()
sys.exit(result)
