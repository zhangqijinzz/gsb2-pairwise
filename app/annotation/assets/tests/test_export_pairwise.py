import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

import openpyxl


EXPORTER = Path(__file__).resolve().parents[1] / "export_pairwise.py"


class ExportPairwiseTests(unittest.TestCase):
    def test_export_pairwise_writes_shared_a_b_and_gsb_columns_without_third_party_runtime(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            payload = {
                "projectName": "Pairwise Demo",
                "submitter": "Reviewer",
                "submittedAt": "2026-09-17",
                "issues": [],
                "cases": [{
                    "taskId": "task-1",
                    "taskName": "加法题",
                    "taskType": "0-1代码生成",
                    "promptDifficulty": "困难",
                    "initialSha": "1" * 40,
                    "snapshotUrl": "https://github.com/example/repo/commit/" + "1" * 40,
                    "pairwise": {
                        "prompt": "实现加法功能",
                        "language": "Python, pytest",
                        "harness": "Claude Code",
                        "harnessVersion": "1.2.3",
                        "os": "MacOS/Linux",
                        "environment": "本地仓库",
                        "runA": self.pairwise_run("A", "a"),
                        "runB": self.pairwise_run("B", "b"),
                        "reviews": [{
                            "id": "review-1", "status": "ready", "conclusion": "A_better",
                            "reason": "A 保留返回值；B 删除返回值，因此 A 更完整。",
                            "aCompletenessScore": 5,
                            "aCompletenessDescription": "A 已交付需求中的返回值处理，产物可以完成预期调用。",
                            "bCompletenessScore": 3,
                            "bCompletenessDescription": "B 的提交缺少返回值处理，交付结果无法覆盖完整调用链。",
                            "sourceHashA": "source-a", "sourceHashB": "source-b",
                        }],
                        "notes": "人工复核完成",
                        "validity": "有效",
                    },
                    "currentPairwiseReview": {
                        "conclusion": "A_better",
                        "reason": "A 保留返回值；B 删除返回值，因此 A 更完整。",
                        "aCompletenessScore": 5,
                        "aCompletenessDescription": "A 已交付需求中的返回值处理，产物可以完成预期调用。",
                        "bCompletenessScore": 3,
                        "bCompletenessDescription": "B 的提交缺少返回值处理，交付结果无法覆盖完整调用链。",
                    },
                }],
            }
            source = root / "input.json"
            source.write_text(json.dumps(payload, ensure_ascii=False), encoding="utf-8")
            output = root / "output"
            proc = subprocess.run(
                [sys.executable, "-S", str(EXPORTER), "--input", str(source), "--output", str(output)],
                text=True, capture_output=True,
            )
            self.assertEqual(proc.returncode, 0, proc.stderr)
            result = json.loads(proc.stdout)
            self.assertEqual(result["rows"], 1)
            sheet = openpyxl.load_workbook(result["outputPath"]).active
            expected_headers = [
                "User Prompt", "任务类型", "任务难度", "语言/框架", "Harness", "Harness 版本",
                "操作系统", "环境可复现等级", "初始环境快照", "A-SessionID", "A-轨迹文件",
                "A-产物快照", "A-运行录屏", "B-SessionID", "B-轨迹文件", "B-产物快照",
                "B-运行录屏", "A-交付完整性", "A-交付完整性描述", "B-交付完整性", "B-交付完整性描述",
                "GSB 结论", "GSB 理由", "有效性", "备注",
            ]
            self.assertEqual([cell.value for cell in sheet[1]][:len(expected_headers)], expected_headers)
            self.assertEqual(sheet["A2"].value, "实现加法功能")
            self.assertEqual(sheet["R2"].value, 5)
            self.assertEqual(sheet["S2"].value, "A 已交付需求中的返回值处理，产物可以完成预期调用。")
            self.assertEqual(sheet["T2"].value, 3)
            self.assertEqual(sheet["U2"].value, "B 的提交缺少返回值处理，交付结果无法覆盖完整调用链。")
            self.assertEqual(sheet["V2"].value, "A 更好")
            self.assertEqual(sheet["D2"].value, "Python, pytest")
            self.assertEqual(sheet["X2"].value, "有效")

            review = payload["cases"][0]["pairwise"]["reviews"][0]
            review["aCompletenessDescription"] = "保存失败后再次成功时只清除保存提示，阻断提示继续保留，连续失败和新编辑场景均已覆盖。"
            review["bCompletenessScore"] = 5
            review["bCompletenessDescription"] = "缺少后加字段的旧记录补上默认值，启动恢复失败单独成一路，随改动提交的 viewModel.test.ts 覆盖失败后继续编辑场景，读取异常时改走备份恢复。"
            source.write_text(json.dumps(payload, ensure_ascii=False), encoding="utf-8")
            accepted = subprocess.run(
                [sys.executable, "-S", str(EXPORTER), "--input", str(source), "--output", str(root / "accepted")],
                text=True, capture_output=True,
            )
            self.assertEqual(accepted.returncode, 0, accepted.stderr or accepted.stdout)

            review["aCompletenessDescription"] = "完成未完成拖拽的收尾清理，尺寸不足与草稿缺失各分支都先清空拖拽起点，正常拖完的一笔仍作为一个障碍提交。"
            review["bCompletenessDescription"] = "缺少拖拽起点或草稿以及尺寸不足时同样走取消逻辑，切换工具与指针取消后不会新增障碍。"
            source.write_text(json.dumps(payload, ensure_ascii=False), encoding="utf-8")
            accepted_state = subprocess.run(
                [sys.executable, "-S", str(EXPORTER), "--input", str(source), "--output", str(root / "accepted-state")],
                text=True, capture_output=True,
            )
            self.assertEqual(accepted_state.returncode, 0, accepted_state.stderr or accepted_state.stdout)

            review["aCompletenessDescription"] = "旧格式记录缺少网格尺寸按默认值 24 填充，障碍数组缺失时给空列表，主库记录无法识别成方案时不再当成没有数据，转为从备份取回；主库打不开或读取报错时同样落到备份恢复。"
            source.write_text(json.dumps(payload, ensure_ascii=False), encoding="utf-8")
            accepted_recovery = subprocess.run(
                [sys.executable, "-S", str(EXPORTER), "--input", str(source), "--output", str(root / "accepted-recovery")],
                text=True, capture_output=True,
            )
            self.assertEqual(accepted_recovery.returncode, 0, accepted_recovery.stderr or accepted_recovery.stdout)

            review["aCompletenessDescription"] = "保存时主库与备份一起刷新，避免两份长期不一致；src/viewModel.test.ts 另外补上切换工具、指针取消与正常提交三条回归用例，正常拖完的一笔仍会提交并持久化。"
            source.write_text(json.dumps(payload, ensure_ascii=False), encoding="utf-8")
            accepted_outcomes = subprocess.run(
                [sys.executable, "-S", str(EXPORTER), "--input", str(source), "--output", str(root / "accepted-outcomes")],
                text=True, capture_output=True,
            )
            self.assertEqual(accepted_outcomes.returncode, 0, accepted_outcomes.stderr or accepted_outcomes.stdout)

            payload["cases"][0]["pairwise"]["reviews"][0]["aCompletenessDescription"] = "核心流程已交付，但真实浏览器交互尚未验证。"
            source.write_text(json.dumps(payload, ensure_ascii=False), encoding="utf-8")
            rejected = subprocess.run(
                [sys.executable, "-S", str(EXPORTER), "--input", str(source), "--output", str(root / "rejected")],
                text=True, capture_output=True,
            )
            self.assertNotEqual(rejected.returncode, 0)
            self.assertIn("5 分时", rejected.stdout)

    @staticmethod
    def pairwise_run(side, sha_char):
        return {
            "side": side,
            "branch": side,
            "sessionId": "session-" + side.lower(),
            "tracePath": "/evidence/" + side + "/session.jsonl",
            "turnCount": 1,
            "deliverableSha": sha_char * 40,
            "deliverableUrl": "https://github.com/example/repo/commit/" + sha_char * 40,
            "videoStatus": "ready",
            "videoUrl": "https://example.com/" + side.lower() + ".mp4",
        }


if __name__ == "__main__":
    unittest.main()
