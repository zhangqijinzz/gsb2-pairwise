import { describe, expect, it } from 'vitest';
import { buildContainerCommand } from './containerCommand';

describe('container startup command', () => {
  it('preserves a configured key literally without an interactive prompt', () => {
    const { command } = buildContainerCommand({ taskName: 'cyc-03', taskId: 'p1__feat__label-8815-9', sourcePath: '' }, "fixture'$(false)");
    expect(command).toContain("apikey='fixture'\\''$(false)'");
    expect(command).not.toContain('read -r -s apikey');
  });
  it('uses the question name and fixed cross-type sequence', () => {
    const result = buildContainerCommand({ taskName: 'cyc-03', taskId: 'p1__feat__label-8815-9', sourcePath: '/tasks/cyc-03-feature迭代-9' });
    expect(result.containerName).toBe('cyc03-claude-9');
    expect(result.command).toContain('BASE_DIR="$HOME/cyc03-claude-runs"');
    expect(result.command).toContain('RUN_DIR="$BASE_DIR/run-9"');
    expect(result.command).not.toContain('mktemp');
    expect(result.command).toContain('mkdir "$RUN_DIR"');
    expect(result.command).toContain('-e apikey');
    expect(result.command).not.toContain('sk-');
  });

  it('uses folder sequence for older tasks and normalizes the prefix', () => {
    expect(buildContainerCommand({ taskName: 'CYC-03', taskId: 'legacy', sourcePath: '/tasks/cyc-03-0-1代码生成-1/' }).containerName).toBe('cyc03-claude-1');
  });

  it('creates isolated A and B container identities for pairwise runs', () => {
    const task = { taskName: 'cyc-03', taskId: 'p1__feat__label-8815-9', sourcePath: '/tasks/cyc-03-feature迭代-9' };
    const runA = buildContainerCommand(task, 'key', 'A');
    const runB = buildContainerCommand(task, 'key', 'B');
    expect(runA.containerName).toBe('cyc03-claude-9-a');
    expect(runA.runDirectory).toBe('run-9-a');
    expect(runB.containerName).toBe('cyc03-claude-9-b');
    expect(runB.runDirectory).toBe('run-9-b');
    expect(runA.command).toContain('adminfather/benzhi-claude-code2:20260919');
    expect(runB.command).toContain('adminfather/benzhi-claude-code2:20260919');
  });

  it('rejects missing, conflicting, and unsafe task identity', () => {
    const base = { taskName: 'cyc-03', taskId: 'p1__gen__label-8815-4', sourcePath: '/tasks/cyc-03-0-1代码生成-4' };
    expect(() => buildContainerCommand({ ...base, taskName: '$(touch /tmp/unwanted)' })).toThrow();
    expect(() => buildContainerCommand({ ...base, sourcePath: '/tasks/cyc-03-0-1代码生成-5' })).toThrow();
    expect(() => buildContainerCommand({ ...base, taskId: 'legacy', sourcePath: '' })).toThrow();
  });
});
