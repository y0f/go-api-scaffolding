/* Plays this repository's plain-text asciicast output; no terminal emulation. */
for (const player of document.querySelectorAll(".demo-player")) {
  const output = player.querySelector("[data-output]");
  const play = player.querySelector("[data-play]");
  const restart = player.querySelector("[data-restart]");
  const speed = player.querySelector("[data-speed]");
  const status = player.querySelector("[data-status]");
  const clock = player.querySelector("[data-time]");
  let events = [], duration = 0, position = 0, cursor = 0, previous = 0, playing = false, animation = 0;
  const format = seconds => `${Math.floor(seconds / 60)}:${String(Math.floor(seconds % 60)).padStart(2, "0")}`;
  const label = () => { clock.textContent = `${format(position)} / ${format(duration)}`; };

  function frame(now) {
    if (!playing) return;
    position = Math.min(duration, position + (now - previous) / 1000 * Number(speed.value));
    previous = now;
    while (cursor < events.length && events[cursor][0] <= position) {
      output.append(document.createTextNode(events[cursor++][2].replace(/\r\n/g, "\n")));
    }
    output.parentElement.scrollTop = output.parentElement.scrollHeight;
    label();
    if (position >= duration) {
      playing = false;
      play.textContent = "Replay";
      status.textContent = "Recording complete";
    } else animation = requestAnimationFrame(frame);
  }

  function reset() {
    cancelAnimationFrame(animation);
    playing = false;
    position = cursor = 0;
    output.textContent = "";
    play.textContent = "Play";
    status.textContent = "Ready";
    label();
  }

  play.addEventListener("click", () => {
    if (playing) {
      cancelAnimationFrame(animation);
      playing = false;
      play.textContent = "Resume";
      status.textContent = "Paused";
      return;
    }
    if (position >= duration) reset();
    playing = true;
    previous = performance.now();
    play.textContent = "Pause";
    status.textContent = "Playing";
    animation = requestAnimationFrame(frame);
  });
  restart.addEventListener("click", reset);

  fetch(player.dataset.castUrl).then(response => {
    if (!response.ok) throw new Error("Recording unavailable");
    return response.text();
  }).then(text => {
    const [header, ...lines] = text.trim().split("\n").map(line => JSON.parse(line));
    if (header.version !== 2) throw new Error("Unsupported recording format");
    events = lines.filter(event => event[1] === "o");
    duration = Math.max(header.duration || 0, events.at(-1)?.[0] || 0);
    play.disabled = restart.disabled = false;
    reset();
  }).catch(() => { status.textContent = "Playback unavailable. Read the transcript below."; });
}
