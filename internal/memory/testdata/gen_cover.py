"""Prints cover(T, 96) and pending() from the reference Python tool.

Usage: MEMORY_DIR=<fixture dir> python gen_cover.py > fx.cover.txt
Imports ~/.optmem/memo as a module; never touches the live memory
because MEMORY_DIR must point at a fixture.
"""
import importlib.machinery
import importlib.util
import os
import sys

if not os.environ.get("MEMORY_DIR"):
    sys.exit("set MEMORY_DIR to a fixture dir")
loader = importlib.machinery.SourceFileLoader(
    "memo", os.path.expanduser("~/.optmem/memo"))  # no .py suffix
spec = importlib.util.spec_from_loader("memo", loader)
memo = importlib.util.module_from_spec(spec)
loader.exec_module(memo)

for T in (1, 5, 16, 40, 100, 1000, 5000):
    print("cover %d %s" % (T, " ".join("%d-%d" % b for b in memo.cover(T, 96))))
d = memo.memory_dir()
print("pending %s" % " ".join("%d-%d" % b for b in memo.pending(d, 40)))
print("pending_count %d" % memo.pending_count(d, 40))
