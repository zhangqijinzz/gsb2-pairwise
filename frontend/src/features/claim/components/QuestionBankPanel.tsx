import { useQuestionBank } from '../hooks/useQuestionBank';
import { CustomProjectPickerModal, CustomPromptPreviewModal, SyncToolbar } from './QuestionBankSyncCards';
import QuestionBankList from './QuestionBankList';
import BulkCreateSection from './BulkCreateSection';
import GitLabProjectAdder from './GitLabProjectAdder';
import type { ClaimProjectState } from '../hooks/useClaimProject';

export default function QuestionBankPanel({
  project,
}: {
  project: ClaimProjectState;
}) {
  const { activeProject } = project;

  const qb = useQuestionBank(
    activeProject?.id ?? '',
    activeProject?.questionBankProjectIds ?? '',
  );

  if (!activeProject) {
    return (
      <div className="flex items-center justify-center py-20 text-sm text-stone-400 dark:text-stone-500">
        暂无激活项目，请先在设置中创建并激活项目
      </div>
    );
  }

  return (
    <div className="space-y-5 pb-24">
      <SyncToolbar
        importingLocalSources={qb.importingLocalSources}
        localImportError={qb.localImportError}
        localImportResult={qb.localImportResult}
        onScan={qb.handleScanLocalQuestionBank}
        customProjectImporting={qb.customProjectImporting}
        customProjectScanLoading={qb.customProjectScanLoading}
        customProjectImportError={qb.customProjectImportError}
        customProjectImportResult={qb.customProjectImportResult}
        customProjectPromptDocGenerating={qb.customProjectPromptDocGenerating}
        customProjectPromptDocError={qb.customProjectPromptDocError}
        customProjectPromptDocResult={qb.customProjectPromptDocResult}
        customPromptTaskCreating={qb.customPromptTaskCreating}
        customPromptTaskError={qb.customPromptTaskError}
        customPromptTaskResult={qb.customPromptTaskResult}
        onScanCustomProjects={qb.handleScanCustomProjects}
        onCreateTasksFromGeneratedPromptDocs={qb.handleCreateTasksFromGeneratedPromptDocs}
        onCreateTasksFromPickedPromptDocs={qb.handleCreateTasksFromPickedPromptDocs}
        onImportArchives={qb.handleImportArchivesViaPicker}
        syncing={qb.questionBankSyncing}
        syncError={qb.questionBankSyncError}
        syncResult={qb.questionBankSyncResult}
        configuredGitLabQuestionIds={qb.configuredGitLabQuestionIds}
        onSync={qb.handleSyncGitLabQuestionBank}
        normalizing={qb.normalizing}
        normalizeError={qb.normalizeError}
        normalizeResult={qb.normalizeResult}
        onNormalize={qb.handleNormalize}
      />

      <CustomProjectPickerModal
        open={qb.customProjectPickerOpen}
        scanResult={qb.customProjectScanResult}
        importing={qb.customProjectImporting}
        promptDocGenerating={qb.customProjectPromptDocGenerating}
        error={qb.customProjectImportError}
        promptDocError={qb.customProjectPromptDocError}
        onClose={qb.closeCustomProjectPicker}
        onImport={qb.handleImportSelectedCustomProjects}
      />

      <CustomPromptPreviewModal
        open={qb.customPromptPreviewOpen}
        docs={qb.customPromptPreviewDocs}
        generating={qb.customProjectPromptDocGenerating}
        saving={qb.customPromptPreviewSaving}
        creating={qb.customPromptTaskCreating}
        error={qb.customPromptPreviewError || qb.customPromptTaskError}
        status={qb.customPromptPreviewStatus}
        onClose={qb.closeCustomPromptPreview}
        onSave={qb.handleSaveCustomPromptDocument}
        onRegenerate={qb.handleRegenerateCustomPromptDocument}
        onConfirm={qb.handleConfirmCustomPromptPreview}
      />

      <GitLabProjectAdder
        activeProject={activeProject}
        onSync={qb.handleSyncGitLabQuestionBank}
        syncing={qb.questionBankSyncing}
      />

      <QuestionBankList
        filteredItems={qb.filteredQuestionBankItems}
        selectableFilteredItems={qb.selectableFilteredQuestionBankItems}
        totalCount={qb.questionBankItems.length}
        readyCount={qb.readyQuestionCount}
        selectedCount={qb.selectedQuestionCount}
        selectedIdSet={qb.selectedQuestionIdSet}
        allFilteredSelected={qb.allFilteredSelected}
        filter={qb.questionBankFilter}
        setFilter={qb.setQuestionBankFilter}
        onToggleSelection={qb.toggleQuestionSelection}
        onToggleSelectAll={qb.toggleSelectAllFiltered}
        onSelectAll={qb.selectAllFiltered}
        onClearSelection={qb.clearSelection}
        onInvertSelection={qb.invertSelectionOnFiltered}
        onRefresh={qb.handleRefreshQuestionBankItem}
        refreshingQuestionId={qb.refreshingQuestionId}
        onDelete={(item) => { void qb.handleDeleteQuestionBankItem(item.questionId); }}
        deletingQuestionId={qb.deletingQuestionId}
        deleteError={qb.deleteError}
        loading={qb.questionBankLoading}
        error={qb.questionBankError}
      />

      <BulkCreateSection
        project={project}
        selectedQuestionBankItems={qb.selectedQuestionBankItems}
        selectedQuestionCount={qb.selectedQuestionCount}
        onCreated={qb.reloadQuestionBankItems}
      />
    </div>
  );
}
