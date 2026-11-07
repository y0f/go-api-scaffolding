# Recorded resource workflow

Recorded from [b80dc3e](https://github.com/y0f/gophling/commit/b80dc3e8ca1aa22ac9a629b30b673809fa730ea8)
on 7 November 2026 with Go 1.27.3 on Windows.

This recording adds a Note resource, regenerates bindings, runs the fast check,
and shows the resulting diff. It uses an initialized project with prepared tools
and shared Go caches. It is a command demonstration, not an agent evaluation or
a cold-install benchmark.

<link rel="stylesheet" href="assets/demo.css">
<div class="demo-player" data-cast-url="assets/demo.cast">
  <div class="demo-controls">
    <button type="button" data-play disabled>Play</button>
    <button type="button" data-restart disabled>Restart</button>
    <label>Speed <select data-speed><option value="1">1×</option><option value="2">2×</option><option value="4">4×</option></select></label>
    <span data-time>0:00</span>
    <span data-status role="status">Loading recording…</span>
  </div>
  <pre class="demo-terminal" tabindex="0" aria-label="Recorded command output"><code data-output></code></pre>
</div>
<script src="assets/demo.js" defer></script>

[Read the transcript](assets/demo.txt), [download the recording](assets/demo.cast),
or inspect the [source revision and command timings](assets/demo.json).
The player runs locally in your browser with no external dependencies.

The output is captured from real commands. Timestamps reflect elapsed time;
local paths and credential-shaped values are redacted. Preparation is excluded
from playback and listed separately in the metadata. The fast check covers
generation consistency, lint, unit tests, and build; it does not run Postgres
integration tests or the full release gate.

To repeat it from a committed checkout:

```sh
python evals/record_demo.py --ref COMMIT --output ../gophling-demo
```

The script creates a disposable clone, removes its origin remote, and commits
the prepared fixture locally. It never pushes or changes the source checkout.
Review the output before publishing it. The recording uses the
[asciicast v2 format](https://docs.asciinema.org/manual/asciicast/v2/).
