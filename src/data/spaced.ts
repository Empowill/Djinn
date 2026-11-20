// A read that follows changes, one at a time and spaced: the History tab reads a whole wish (WishService.Snapshot),
// and a busy wish changes several times a second. A read on each change, the one before aborted, never ended and kept
// the server reading (W149).

export interface Spaced {
  // changed says what was read is stale: a read starts once the one going ends, spacing after the last one started.
  // Changes meanwhile make one read.
  changed(): void;
  // stop drops the read to come; a read going ends unseen by spaced.
  stop(): void;
}

// spaced starts read now, then again as changed() asks, spacing milliseconds apart at least, never two at once.
export function spaced(
  read: () => Promise<unknown>,
  spacing: number,
  now: () => number = Date.now,
): Spaced {
  let reading = false;
  let stale = false;
  let stopped = false;
  let last = 0;
  let timer: ReturnType<typeof setTimeout> | undefined;
  const schedule = () => {
    if (!stale || reading || stopped || timer !== undefined) return;
    timer = setTimeout(start, Math.max(0, last + spacing - now()));
  };
  const start = () => {
    timer = undefined;
    if (stopped) return;
    reading = true;
    stale = false;
    last = now();
    let done: Promise<unknown>;
    try {
      done = read();
    } catch (err) {
      done = Promise.reject(err);
    }
    done
      .catch(() => {}) // The reader shows its own errors.
      .finally(() => {
        reading = false;
        schedule();
      });
  };
  start();
  return {
    changed() {
      stale = true;
      schedule();
    },
    stop() {
      stopped = true;
      clearTimeout(timer);
      timer = undefined;
    },
  };
}
