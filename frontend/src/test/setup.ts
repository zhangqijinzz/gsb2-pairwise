import '@testing-library/jest-dom';

function createMemoryStorage(): Storage {
  const values = new Map<string, string>();
  return {
    get length() { return values.size; },
    clear: () => values.clear(),
    getItem: (key) => values.get(key) ?? null,
    key: (index) => Array.from(values.keys())[index] ?? null,
    removeItem: (key) => { values.delete(key); },
    setItem: (key, value) => { values.set(key, String(value)); },
  };
}

// Node can expose an unavailable experimental localStorage in the test worker.
// Keep component tests deterministic without reading that getter first.
Object.defineProperty(window, 'localStorage', {
  configurable: true,
  value: createMemoryStorage(),
});
