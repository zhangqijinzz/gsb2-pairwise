type TaskIdentity = { taskName: string; taskId: string; sourcePath: string };
type PairwiseSide = 'A' | 'B';

export function buildContainerCommand(task: TaskIdentity, apiKey = '', side?: PairwiseSide) {
  const name = task.taskName.trim();
  if (!/^[a-zA-Z0-9][a-zA-Z0-9 _-]*$/.test(name)) {
    throw new Error('大题名称需使用英文字母、数字、空格或连字符，才能生成容器名称。');
  }
  const prefix = name.toLowerCase().replace(/[ _-]+/g, '');
  const idSequence = task.taskId.match(/(?:^|__)label-\d+-(\d+)$/)?.[1];
  const folder = task.sourcePath.replace(/[\\/]+$/, '').split(/[\\/]/).pop() ?? '';
  const folderSequence = folder.toLowerCase().startsWith(`${name.toLowerCase()}-`) ? folder.match(/-(\d+)$/)?.[1] : undefined;
  if (idSequence && folderSequence && Number(idSequence) !== Number(folderSequence)) {
    throw new Error('题目编号与目录编号不一致，请先核对题目目录。');
  }
  const sequence = Number(idSequence ?? folderSequence);
  if (!Number.isSafeInteger(sequence) || sequence < 1) {
    throw new Error('尚未找到题目的固定编号，请先完成题目创建。');
  }
  const suffix = side ? `-${side.toLowerCase()}` : '';
  const containerName = `${prefix}-claude-${sequence}${suffix}`;
  const runDirectory = `run-${sequence}${suffix}`;
  const command = [
    '(',
    `CONTAINER_NAME="${containerName}"`,
    `BASE_DIR="$HOME/${prefix}-claude-runs"`,
    `RUN_DIR="$BASE_DIR/${runDirectory}"`,
    '',
    ...(apiKey ? [`apikey='${apiKey.replace(/'/g, "'\\''")}'`] : [
    'if [ -z "${apikey:-}" ]; then',
    '  printf "请输入容器 API Key（输入不回显）："',
    '  IFS= read -r -s apikey',
    '  printf "\\n"',
    'fi',
    ]),
    '[ -n "${apikey:-}" ] || { printf "API Key 不能为空\\n"; exit 1; }',
    'export apikey',
    '',
    'mkdir -p "$BASE_DIR" && \\',
    'mkdir "$RUN_DIR" && \\',
    'mkdir "$RUN_DIR/workspace" && \\',
    'printf \'容器：%s\\n本题本地目录：%s\\n\' "$CONTAINER_NAME" "$RUN_DIR" && \\',
    'docker run -it --init --restart=no --cap-drop ALL --security-opt no-new-privileges --name "$CONTAINER_NAME" --mount "type=bind,src=$RUN_DIR/workspace,dst=/workspace" -e apikey adminfather/benzhi-claude-code2:20260919',
    ')',
  ].join('\n');
  return { containerName, baseDirectory: `$HOME/${prefix}-claude-runs`, runDirectory, command };
}
