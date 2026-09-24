import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { LlmProvidersPanel } from './SettingsPanels';

describe('LlmProvidersPanel', () => {
  it('offers a dedicated DeepSeek V4 Flash setup action', () => {
    const onCreateDeepSeekProvider = vi.fn();
    render(
      <LlmProvidersPanel
        llmLoadError=""
        llmSaveStatus="idle"
        llmProviders={[]}
        testingProviderId=""
        providerTestStatus={{}}
        onCreateDeepSeekProvider={onCreateDeepSeekProvider}
        onCreateProvider={vi.fn()}
        onCreateAcpProvider={vi.fn()}
        onSetDefaultProvider={vi.fn()}
        onTestProvider={vi.fn()}
        onEditProvider={vi.fn()}
        onDeleteProvider={vi.fn()}
      />,
    );

    fireEvent.click(screen.getByRole('button', { name: '添加 DeepSeek V4 Flash' }));
    expect(onCreateDeepSeekProvider).toHaveBeenCalledTimes(1);
    expect(screen.getByText(/统一用于提示词生成、润色、单题审核和批量审核/)).toBeInTheDocument();
  });
});
