import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { it, expect, vi } from 'vitest';
import { AnnotationExportSettings } from './AnnotationExportSettings';
const config = vi.hoisted(() => ({ getConfig: vi.fn(), setConfig: vi.fn() }));
vi.mock('../../../api/config', () => config);

it('loads and saves the single shared export directory and rejects relative paths', async () => {
  config.getConfig.mockImplementation((key: string) => Promise.resolve(key === 'annotation_export_directory' ? '/exports/old' : '4'));
  config.setConfig.mockResolvedValue(undefined);
  render(<AnnotationExportSettings />);
  const field = await screen.findByDisplayValue('/exports/old');
  fireEvent.change(field, { target: { value: '/exports/new' } });
  fireEvent.change(screen.getByLabelText('批量审核并发数'), { target: { value: '6' } });
  fireEvent.click(screen.getByRole('button', { name: '保存审核与导出设置' }));
  await waitFor(() => expect(config.setConfig).toHaveBeenCalledWith('annotation_export_directory', '/exports/new'));
  expect(config.setConfig).toHaveBeenCalledWith('annotation_review_concurrency', '6');
  await screen.findByText('已保存导出位置和批量审核并发数');
  config.setConfig.mockClear();
  fireEvent.change(field, { target: { value: 'relative/path' } });
  fireEvent.click(screen.getByRole('button', { name: '保存审核与导出设置' }));
  expect(config.setConfig).not.toHaveBeenCalled();
  expect(screen.getByRole('status')).toHaveTextContent('请填写本机绝对路径');
});
