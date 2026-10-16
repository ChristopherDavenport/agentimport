// Package cline reads the task directories of classic Cline (the VS
// Code extension before 4.0, and the standalone hosts that kept its
// layout) and of Roo Code, which forked from it: one directory per task
// under <storage>/tasks/<id>, holding the model's context as a JSON
// array of Anthropic messages (api_conversation_history.json), the chat
// view's messages (ui_messages.json), and, per variant, the task's
// metadata, classic's context-window edits (context_history.json) and
// Roo's index entry (history_item.json). Classic's index entries are
// in <storage>/state/taskHistory.json. It imports the standard library
// alone. Cline 4's SDK session store is another format.
//
// The files are whole JSON documents, rewritten on every change, not
// logs. Each array is read element by element: an element that is not
// an object is kept with its error and its neighbours still read, and
// an array cut short keeps the elements that end before the cut. Every
// object keeps the members it does not declare and writes them back, so
// an element read and written again is the same JSON value.
//
// [ReadTask] reads a task directory, [ParseTask] its files' contents;
// [FindSessions] lists the task directories under a storage directory.
// Neither format records a version; the variant is told by the storage
// directory's name, else by shapes only one of them writes, and the
// task says which. [ParseXMLTools] reads the tool calls a model wrote
// as XML in its text. The reader does not interpret: rebuilding what
// the model saw, and converting to the Agent Session Format, are the
// convert subpackage's; docs/formats/cline.md is the mapping.
package cline
