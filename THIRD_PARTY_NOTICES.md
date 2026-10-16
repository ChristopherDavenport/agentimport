# Third-party notices

agentimport is Copyright (c) 2026 Christopher Davenport and licensed
under the MIT License; see [LICENSE](LICENSE).

Reading another program's session files needs no notice, and most of
this module only reads them. Some files, though, port code of the
programs whose logs they read, so that a converted session holds what
those programs sent the model, byte for byte: how a program rebuilds
the model's context, renders a tool result, frames a summary or mints
an ID. Those portions remain under their upstream licenses, listed
below, and each file that holds one says so in a comment at its top.
Every port has been modified: translated from TypeScript, Python or
Rust into Go and fitted to this module's types.

A file listed as **translated** follows the upstream code's structure
and control flow. A file listed as **strings only** copies short texts
the upstream program writes, so the output matches it, and nothing
else.

## OpenHands software agent SDK

- Repository: https://github.com/OpenHands/software-agent-sdk
- License: MIT
- Copyright: Copyright (c) 2026 OpenHands contributors (the LICENSE at
  `d5995133` and `v1.28.1`); Copyright (c) 2025 All Hands AI (the
  LICENSE at `1.0.0`)
- Read at: `d5995133` (2026-10-15, two commits after `v1.55.0`), with
  the tags `v1.28.1` (`edaac806`) and `1.0.0` (`a612c0a6`) and tags
  between them where shapes changed

| File | Kind | Ported from |
| --- | --- | --- |
| `openhands/convert/view.go` | translated | the view and its four properties: `openhands-sdk/openhands/sdk/context/view/view.py` and `properties/` (`batch_atomicity.py`, `observation_uniqueness.py`, `tool_call_matching.py`, `tool_loop_atomicity.py`); `context/view.py` of SDK 1.0 to 1.9 |
| `openhands/convert/era.go` | translated | `View.enforce_properties` (`context/view/view.py`) and the enforcement of earlier releases |
| `openhands/convert/render.go` | translated | each observation class's `to_llm_content` (`openhands-sdk/openhands/sdk/tool/schema.py`, `openhands-tools/openhands/tools/{terminal,task,browser_use,tom_consult}/definition.py`), `maybe_truncate` (`openhands-sdk/openhands/sdk/utils/truncate.py`), `Message._maybe_truncate_tool_text` (`llm/message.py`), `detect_image_mime_type` |
| `openhands/convert/events.go` | strings only | the rejection text (`event/llm_convertible/observation.py`) and the content-filter message (`agent/agent.py`) |

The MIT License's permission notice, as in the LICENSE at `d5995133`.
The LICENSE at `1.0.0` is the same but for its copyright line,
Copyright (c) 2025 All Hands AI.

```text
MIT License

Copyright (c) 2026 OpenHands contributors

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

## OpenCode

- Repository: https://github.com/anomalyco/opencode
- License: MIT
- Copyright: Copyright (c) 2025 opencode (the LICENSE at every commit
  and tag read)
- Read at: `a448d5b` (`dev`, 2.0.30, 2026-10-15) and the tags `v2.0.0`,
  `v1.19.1`, `v1.14.40`, `v1.14.39`, `v1.14.19`, `v1.14.18`, `v1.3.16`,
  `v1.3.15`, `v1.2.0` and `v1.1.65`

| File | Kind | Ported from |
| --- | --- | --- |
| `opencode/convert/v1.go` | translated | `toModelMessages` (`packages/opencode/src/session/message-v2.ts`, each V1 tag): which parts a message sends, the texts sent in place of compaction and subtask parts and of an interrupted or pruned call, which assistant messages are skipped, an empty text beside signed reasoning sent as a space; the refusal texts of `packages/core/src/v1/permission.ts` |
| `opencode/convert/compaction.go` | translated | `filterCompacted` (`message-v2.ts`) |
| `opencode/convert/payload.go` | translated | `toModelOutput` (`message-v2.ts`) |
| `opencode/convert/v2.go` | translated | the attachment rendering of `packages/core/src/session/runner/to-llm-message.ts` (`textAttachment`, `directoryAttachment`, `attachmentContent`, `userAttachmentContent`) and its text for a change of directory, at `a448d5b` |
| `opencode/convert/session.go` | translated | `syntheticID` (`packages/core/src/database/v1-migration.bun.ts`, at `a448d5b`) |

The MIT License's permission notice, as in the LICENSE at `a448d5b`:

```text
MIT License

Copyright (c) 2025 opencode

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

## Cline

- Repository: https://github.com/cline/cline
- License: Apache-2.0
- Copyright: Copyright 2026 Cline Bot Inc. (the LICENSE's appendix, at
  both commits read)
- Read at: `c8f808b5` (v4.1.24, 2026-10-15), with the history of the
  files below back to v4.0.0, for the Cline SDK; `v3.89.2` (`e1bdeeff`,
  2026-06-11) for the classic task

| File | Kind | Ported from |
| --- | --- | --- |
| `clinesdk/convert/project.go` | translated | `MessageBuilder.buildForApi` and its helpers (`sdk/packages/core/src/session/services/message-builder.ts`, at v4.0.0 and v4.1.24): `truncateMiddleByChars`, `truncateToolResultContent`, `TOOL_CALL_MARKUP_PATTERN`, the read locators, `replaceOutdatedReadContent`, `estimateOutdatedReclaimBytes`, `splitToolResultMedia`, the cache preview, and their texts |
| `clinesdk/convert/reads.go` | translated | `MessageBuilder`'s outdated-read rewrites (`commitOutdatedRewrites`, `resolveToolName`) |
| `clinesdk/convert/payload.go` | translated | `toAiSdkToolResultOutput`, `stripImagesFromOutput`, `isAiSdkContentBlockArray` and their texts (`sdk/packages/shared/src/llms/ai-sdk-format.ts`), `formatFileContentBlock` |
| `clinesdk/text.go` | translated | `normalizeUserInput`, `stripModeNotices` and `formatDisplayUserInput` (`sdk/packages/shared/src/prompt/format.ts`) |
| `clinesdk/compaction.go` | translated | `sourcePrefixHash`, `messageBoundaryKey`, `projectSessionCompactionState` and `SessionCompactionStateSchema` (`sdk/packages/core/src/session/models/session-compaction.ts`) |
| `clinesdk/graph.go` | translated | the sub-session IDs (`sdk/packages/core/src/session/models/session-graph.ts`) |
| `clinesdk/convert/session.go` | strings only | the legacy warning (`apps/vscode/src/sdk/legacy-task-handling.ts`), `TOOL_REJECTION_SUFFIX`, `EMPTY_CONTENT_TEXT` |
| `cline/convert/walk.go` | strings only | the texts the classic task sends in place of context (`apps/vscode/src/core/prompts/responses.ts`, `core/context/context-management/ContextManager.ts`), at `v3.89.2` |

The classic converter's deleted range and tool pairing (`ContextManager`)
are written from reading it, not ported.

## Roo Code

- Repository: https://github.com/RooCodeInc/Roo-Code
- License: Apache-2.0
- Copyright: Copyright 2025 Roo Code, Inc. (the LICENSE's appendix, at
  each commit read)
- Read at: `b867ec91` (v3.54.0, 2026-05-15), with
  `src/core/condense/index.ts` at `v3.40.0` (`85055454`) and `v3.30.0`
  (`00518662`)

| File | Kind | Ported from |
| --- | --- | --- |
| `cline/convert/events.go` | translated | `getEffectiveApiHistory` (`src/core/condense/index.ts`, at `b867ec91`) and `getMessagesSinceLastSummary` (the same file, at `v3.40.0`), in `rooSpec`; `N_MESSAGES_TO_KEEP` |

## OpenAI Codex

- Repository: https://github.com/openai/codex
- License: Apache-2.0
- Copyright: Copyright 2025 OpenAI (the LICENSE's appendix and NOTICE)
- Read at: `7ae831991792744434027c431dfbc7e81dd5c12c` (2026-10-15)

| File | Kind | Ported from |
| --- | --- | --- |
| `codex/convert/classify.go` | translated | how Codex tells its own messages from a person's: `codex-rs/core/src/event_mapping.rs`, `core/src/context/contextual_user_message.rs`, `context-fragments/src/fragment.rs`, `is_user_turn_boundary` (`core/src/context_manager/history.rs`), `InterAgentCommunication::from_message_content` (`protocol/src/protocol.rs`), and the compaction summary's prefix (`prompts/templates/compact/summary_prefix.md`) |
| `codex/convert/window.go` | translated | `build_compacted_history` (`codex-rs/core/src/compact.rs`), `drop_last_n_user_turns` and `trim_pre_turn_context_updates` (`core/src/context_manager/history.rs`) |

Codex ships a NOTICE file. Its text, at the commit read:

```text
OpenAI Codex
Copyright 2025 OpenAI

This project includes code derived from [Ratatui](https://github.com/ratatui/ratatui), licensed under the MIT license.
Copyright (c) 2016-2022 Florian Dehau
Copyright (c) 2023-2025 The Ratatui Developers
```

None of the Ratatui-derived code it credits is among the code ported
here.

## pi

- Repository: https://github.com/earendil-works/pi
- License: MIT
- Copyright: Copyright (c) 2025 Mario Zechner
- Read at: `v1.0.1` (`a7229ddc`), as published in the npm packages
  `@earendil-works/pi-coding-agent` 1.0.1 and `@earendil-works/pi-ai`
  1.0.1

| File | Kind | Ported from |
| --- | --- | --- |
| `pi/convert/message.go` | translated | `bashExecutionToText` and the summary framing (`COMPACTION_SUMMARY_PREFIX` and the like) of `packages/coding-agent/src/core/messages.ts` |
| `pi/convert/config.go` | translated | `getCurrentSystemMessage` and `getCurrentTools` (`packages/ai/src/utils/transcript.ts`) |

The MIT License's permission notice, as in the LICENSE at `v1.0.1`:

```text
MIT License

Copyright (c) 2025 Mario Zechner

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

## Credits

These converters match the behaviour of code they were written against,
read but not ported; no code of theirs is included.

- **Vercel AI SDK** (https://github.com/vercel/ai, Apache-2.0, Copyright
  2023 Vercel, Inc.), at `ai@6.0.168` and `ai@5.0.124`, with
  `@ai-sdk/anthropic@3.0.111`, `@ai-sdk/openai@3.0.88` and
  `@ai-sdk/google@3.0.73`: `opencode/convert` splits an assistant
  message into model calls at each step start, as `convertToModelMessages`
  (`packages/ai/src/ui/convert-to-model-messages.ts`) does, and follows
  the providers on which reasoning they send back.
- **Harbor** (https://github.com/laude-institute/harbor, now
  `harbor-framework/harbor`, Apache-2.0), at `2ede27fb`: `atif/convert`
  rebuilds the context Terminus 2 continues from after it summarises
  (`src/harbor/agents/terminus_2/terminus_2.py`, `_summarize`), reads
  what Harbor's Claude Code adapter records of sub-agents and raw tool
  results (`agents/installed/claude_code.py`), and pairs mini-swe-agent's
  results with its calls in order (`mini_swe_agent.py`). Harbor's
  LICENSE leaves its copyright line as the template's placeholder; its
  `pyproject.toml` names Alex Shaw as author and its `CITATION.cff` the
  Harbor Framework Team.

The Claude Code converter is written from transcripts and the Agent
SDK's published typings, and the format packages only read files; none
of them needs a notice.

## Apache License 2.0

The Cline, Roo Code and Codex portions are licensed under the Apache
License, Version 2.0:

```text
                                 Apache License
                           Version 2.0, January 2004
                        http://www.apache.org/licenses/

   TERMS AND CONDITIONS FOR USE, REPRODUCTION, AND DISTRIBUTION

   1. Definitions.

      "License" shall mean the terms and conditions for use, reproduction,
      and distribution as defined by Sections 1 through 9 of this document.

      "Licensor" shall mean the copyright owner or entity authorized by
      the copyright owner that is granting the License.

      "Legal Entity" shall mean the union of the acting entity and all
      other entities that control, are controlled by, or are under common
      control with that entity. For the purposes of this definition,
      "control" means (i) the power, direct or indirect, to cause the
      direction or management of such entity, whether by contract or
      otherwise, or (ii) ownership of fifty percent (50%) or more of the
      outstanding shares, or (iii) beneficial ownership of such entity.

      "You" (or "Your") shall mean an individual or Legal Entity
      exercising permissions granted by this License.

      "Source" form shall mean the preferred form for making modifications,
      including but not limited to software source code, documentation
      source, and configuration files.

      "Object" form shall mean any form resulting from mechanical
      transformation or translation of a Source form, including but
      not limited to compiled object code, generated documentation,
      and conversions to other media types.

      "Work" shall mean the work of authorship, whether in Source or
      Object form, made available under the License, as indicated by a
      copyright notice that is included in or attached to the work
      (an example is provided in the Appendix below).

      "Derivative Works" shall mean any work, whether in Source or Object
      form, that is based on (or derived from) the Work and for which the
      editorial revisions, annotations, elaborations, or other modifications
      represent, as a whole, an original work of authorship. For the purposes
      of this License, Derivative Works shall not include works that remain
      separable from, or merely link (or bind by name) to the interfaces of,
      the Work and Derivative Works thereof.

      "Contribution" shall mean any work of authorship, including
      the original version of the Work and any modifications or additions
      to that Work or Derivative Works thereof, that is intentionally
      submitted to Licensor for inclusion in the Work by the copyright owner
      or by an individual or Legal Entity authorized to submit on behalf of
      the copyright owner. For the purposes of this definition, "submitted"
      means any form of electronic, verbal, or written communication sent
      to the Licensor or its representatives, including but not limited to
      communication on electronic mailing lists, source code control systems,
      and issue tracking systems that are managed by, or on behalf of, the
      Licensor for the purpose of discussing and improving the Work, but
      excluding communication that is conspicuously marked or otherwise
      designated in writing by the copyright owner as "Not a Contribution."

      "Contributor" shall mean Licensor and any individual or Legal Entity
      on behalf of whom a Contribution has been received by Licensor and
      subsequently incorporated within the Work.

   2. Grant of Copyright License. Subject to the terms and conditions of
      this License, each Contributor hereby grants to You a perpetual,
      worldwide, non-exclusive, no-charge, royalty-free, irrevocable
      copyright license to reproduce, prepare Derivative Works of,
      publicly display, publicly perform, sublicense, and distribute the
      Work and such Derivative Works in Source or Object form.

   3. Grant of Patent License. Subject to the terms and conditions of
      this License, each Contributor hereby grants to You a perpetual,
      worldwide, non-exclusive, no-charge, royalty-free, irrevocable
      (except as stated in this section) patent license to make, have made,
      use, offer to sell, sell, import, and otherwise transfer the Work,
      where such license applies only to those patent claims licensable
      by such Contributor that are necessarily infringed by their
      Contribution(s) alone or by combination of their Contribution(s)
      with the Work to which such Contribution(s) was submitted. If You
      institute patent litigation against any entity (including a
      cross-claim or counterclaim in a lawsuit) alleging that the Work
      or a Contribution incorporated within the Work constitutes direct
      or contributory patent infringement, then any patent licenses
      granted to You under this License for that Work shall terminate
      as of the date such litigation is filed.

   4. Redistribution. You may reproduce and distribute copies of the
      Work or Derivative Works thereof in any medium, with or without
      modifications, and in Source or Object form, provided that You
      meet the following conditions:

      (a) You must give any other recipients of the Work or
          Derivative Works a copy of this License; and

      (b) You must cause any modified files to carry prominent notices
          stating that You changed the files; and

      (c) You must retain, in the Source form of any Derivative Works
          that You distribute, all copyright, patent, trademark, and
          attribution notices from the Source form of the Work,
          excluding those notices that do not pertain to any part of
          the Derivative Works; and

      (d) If the Work includes a "NOTICE" text file as part of its
          distribution, then any Derivative Works that You distribute must
          include a readable copy of the attribution notices contained
          within such NOTICE file, excluding those notices that do not
          pertain to any part of the Derivative Works, in at least one
          of the following places: within a NOTICE text file distributed
          as part of the Derivative Works; within the Source form or
          documentation, if provided along with the Derivative Works; or,
          within a display generated by the Derivative Works, if and
          wherever such third-party notices normally appear. The contents
          of the NOTICE file are for informational purposes only and
          do not modify the License. You may add Your own attribution
          notices within Derivative Works that You distribute, alongside
          or as an addendum to the NOTICE text from the Work, provided
          that such additional attribution notices cannot be construed
          as modifying the License.

      You may add Your own copyright statement to Your modifications and
      may provide additional or different license terms and conditions
      for use, reproduction, or distribution of Your modifications, or
      for any such Derivative Works as a whole, provided Your use,
      reproduction, and distribution of the Work otherwise complies with
      the conditions stated in this License.

   5. Submission of Contributions. Unless You explicitly state otherwise,
      any Contribution intentionally submitted for inclusion in the Work
      by You to the Licensor shall be under the terms and conditions of
      this License, without any additional terms or conditions.
      Notwithstanding the above, nothing herein shall supersede or modify
      the terms of any separate license agreement you may have executed
      with Licensor regarding such Contributions.

   6. Trademarks. This License does not grant permission to use the trade
      names, trademarks, service marks, or product names of the Licensor,
      except as required for reasonable and customary use in describing the
      origin of the Work and reproducing the content of the NOTICE file.

   7. Disclaimer of Warranty. Unless required by applicable law or
      agreed to in writing, Licensor provides the Work (and each
      Contributor provides its Contributions) on an "AS IS" BASIS,
      WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or
      implied, including, without limitation, any warranties or conditions
      of TITLE, NON-INFRINGEMENT, MERCHANTABILITY, or FITNESS FOR A
      PARTICULAR PURPOSE. You are solely responsible for determining the
      appropriateness of using or redistributing the Work and assume any
      risks associated with Your exercise of permissions under this License.

   8. Limitation of Liability. In no event and under no legal theory,
      whether in tort (including negligence), contract, or otherwise,
      unless required by applicable law (such as deliberate and grossly
      negligent acts) or agreed to in writing, shall any Contributor be
      liable to You for damages, including any direct, indirect, special,
      incidental, or consequential damages of any character arising as a
      result of this License or out of the use or inability to use the
      Work (including but not limited to damages for loss of goodwill,
      work stoppage, computer failure or malfunction, or any and all
      other commercial damages or losses), even if such Contributor
      has been advised of the possibility of such damages.

   9. Accepting Warranty or Additional Liability. While redistributing
      the Work or Derivative Works thereof, You may choose to offer,
      and charge a fee for, acceptance of support, warranty, indemnity,
      or other liability obligations and/or rights consistent with this
      License. However, in accepting such obligations, You may act only
      on Your own behalf and on Your sole responsibility, not on behalf
      of any other Contributor, and only if You agree to indemnify,
      defend, and hold each Contributor harmless for any liability
      incurred by, or claims asserted against, such Contributor by reason
      of your accepting any such warranty or additional liability.

   END OF TERMS AND CONDITIONS
```
