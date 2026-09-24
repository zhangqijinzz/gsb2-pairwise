import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { CustomProjectPickerModal, SyncToolbar } from './QuestionBankSyncCards';

function setup() {
  const onImport = vi.fn();
  render(<CustomProjectPickerModal open importing={false} promptDocGenerating={false} error="" promptDocError="" onClose={vi.fn()} onImport={onImport} scanResult={{ projectId: 'batch', projectName: 'Batch', rootPath: '/projects', prefixes: 'cyc', totalCount: 1, skippedCount: 0, candidates: [{ name: 'cyc-05', path: '/projects/cyc-05', questionId: 5, targetPath: '/target' }] }} />);
  fireEvent.click(screen.getByRole('checkbox'));
  return onImport;
}

describe('custom document quantities', () => {
  it('defaults to 22 hard tasks across the three enabled task types', () => {
    setup();
    expect(screen.getByRole('spinbutton', { name: '0-1代码生成' })).toHaveValue(10);
    expect(screen.getByRole('spinbutton', { name: 'Feature迭代' })).toHaveValue(10);
    expect(screen.getByRole('spinbutton', { name: 'Bug修复' })).toHaveValue(2);
    expect(screen.getByRole('spinbutton', { name: '困难' })).toHaveValue(20);
    expect(screen.getByRole('spinbutton', { name: '地狱' })).toHaveValue(2);
    expect(screen.queryByRole('spinbutton', { name: '一般' })).not.toBeInTheDocument();
    expect(screen.queryByRole('spinbutton', { name: '工程化' })).not.toBeInTheDocument();
  });

  it('submits editable quantities while keeping disabled types at zero', () => {
    const onImport = setup();
    fireEvent.change(screen.getByRole('spinbutton', { name: '0-1代码生成' }), { target: { value: '0' } });
    fireEvent.change(screen.getByRole('spinbutton', { name: 'Feature迭代' }), { target: { value: '3' } });
    fireEvent.change(screen.getByRole('spinbutton', { name: 'Bug修复' }), { target: { value: '2' } });
    fireEvent.change(screen.getByRole('spinbutton', { name: '困难' }), { target: { value: '3' } });
    fireEvent.change(screen.getByRole('spinbutton', { name: '地狱' }), { target: { value: '2' } });
    for (const name of ['代码理解', '代码测试', '代码重构']) {
      expect(screen.getByRole('spinbutton', { name })).toBeDisabled();
      expect(screen.getByRole('spinbutton', { name })).toHaveValue(0);
    }
    fireEvent.click(screen.getByRole('button', { name: '导入并生成文档' }));
    expect(onImport).toHaveBeenCalledWith(['cyc-05'], { codeGen: 0, feature: 3, bugFix: 2, difficult: 3, hell: 2 });
  });

  it('blocks empty, fractional and negative quantities', () => {
    const onImport = setup();
    for (const value of ['', '-1', '1.5']) {
      fireEvent.change(screen.getByRole('spinbutton', { name: 'Bug修复' }), { target: { value } });
      expect(screen.getByRole('button', { name: '导入并生成文档' })).toBeDisabled();
    }
    expect(onImport).not.toHaveBeenCalled();
  });

  it('requires difficulty quantities to equal the generated task total', () => {
    setup();
    fireEvent.change(screen.getByRole('spinbutton', { name: '困难' }), { target: { value: '11' } });
    expect(screen.getByText(/难度数量合计 13 题，与题型总数 22 题不一致/)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '导入并生成文档' })).toBeDisabled();
  });
});

describe('custom project import errors', () => {
  it('shows the failed project and its concrete error message', () => {
    render(
      <SyncToolbar
        importingLocalSources={false}
        localImportError=""
        localImportResult={null}
        onScan={vi.fn()}
        customProjectImporting={false}
        customProjectScanLoading={false}
        customProjectImportError=""
        customProjectImportResult={{
          projectId: 'batch',
          projectName: 'Batch',
          importedCount: 0,
          skippedCount: 0,
          errorCount: 1,
          removedCount: 0,
          details: [{
            name: 'xh-01',
            kind: 'custom_directory',
            path: '/projects/xh-01',
            status: 'error',
            message: '读取 node_modules/demo 失败：is a directory',
          }],
        }}
        customProjectPromptDocGenerating={false}
        customProjectPromptDocError=""
        customProjectPromptDocResult={null}
        customPromptTaskCreating={false}
        customPromptTaskError=""
        customPromptTaskResult={null}
        onScanCustomProjects={vi.fn()}
        onCreateTasksFromGeneratedPromptDocs={vi.fn()}
        onCreateTasksFromPickedPromptDocs={vi.fn()}
        onImportArchives={vi.fn()}
        syncing={false}
        syncError=""
        syncResult={null}
        configuredGitLabQuestionIds={[]}
        onSync={vi.fn()}
        normalizing={false}
        normalizeError=""
        normalizeResult={null}
        onNormalize={vi.fn()}
      />,
    );

    expect(screen.getByText('xh-01：读取 node_modules/demo 失败：is a directory')).toBeInTheDocument();
  });
});
