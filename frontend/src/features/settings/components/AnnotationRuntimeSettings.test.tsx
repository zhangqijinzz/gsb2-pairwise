import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { AnnotationRuntimeSettings } from './AnnotationRuntimeSettings';

const config = vi.hoisted(() => ({
  getConfig: vi.fn(),
  setConfig: vi.fn(),
  getAnnotationSettings: vi.fn(),
  saveAnnotationSettings: vi.fn(),
}));
vi.mock('../../../api/config', () => config);

it('loads and saves the global review engine and container API key', async () => {
  config.getConfig.mockResolvedValue('xh04，cyc xh04');
  config.setConfig.mockResolvedValue(undefined);
  config.getAnnotationSettings.mockResolvedValue({ reviewEngine: 'codex', hasContainerApiKey: true });
  config.saveAnnotationSettings.mockResolvedValue(undefined);
  render(<AnnotationRuntimeSettings />);

  expect(await screen.findByDisplayValue('Codex CLI')).toBeInTheDocument();
  expect(screen.getByText('容器 API Key 已保存')).toBeInTheDocument();
  expect(screen.getByLabelText('容器排序前缀')).toHaveValue('xh04，cyc xh04');
  fireEvent.change(screen.getByLabelText('审核引擎'), { target: { value: 'deepseek' } });
  fireEvent.change(screen.getByLabelText('容器 API Key'), { target: { value: 'new-container-key' } });
  fireEvent.change(screen.getByLabelText('容器排序前缀'), { target: { value: ' XH04，cyc xh04 ' } });
  fireEvent.click(screen.getByRole('button', { name: '保存审核与容器设置' }));

  await waitFor(() => expect(config.saveAnnotationSettings).toHaveBeenCalledWith('deepseek', 'new-container-key'));
  expect(config.setConfig).toHaveBeenCalledWith('annotation_container_sort_prefixes', 'xh04,cyc');
  expect(await screen.findByRole('status')).toHaveTextContent('已保存');
});
