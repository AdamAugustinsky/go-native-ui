// Command probe-p5 is phase 8's probe P5 (docs/PHASE8-PLAN.md, D67): does a
// hosted RichEdit control give the composer what it needs on Windows?
// Colour emoji, IME messages, undo grouping through TOM, EN_REQUESTRESIZE
// heights, a caret rectangle, spell checking and the OLE callback that
// routes pasted data.
//
// It decides D67's details (and whether the windowless RichEdit refinement
// is needed early): the composer's height comes from EN_REQUESTRESIZE,
// Replace is one undo step through a TOM edit collection, pasted images and
// files go through IRichEditOleCallback::QueryAcceptData.
//
// # What it does
//
// On the main thread, locked and made an OLE STA (OleInitialize, phase 8's
// Windows rule), it loads msftedit.dll from System32 and hosts a
// RICHEDIT50W (MSFTEDIT_CLASS) multi-line control in a top-level window,
// then:
//
//   - emoji: "A😀B☺C" at 24 pt with B red, measured three ways: the
//     default font binding, the emoji's face set to Segoe UI Emoji, and that
//     plus EM_SETTYPOGRAPHYOPTIONS(TO_ADVANCEDTYPOGRAPHY |
//     TO_DEFAULTCOLOREMOJI | TO_DISPLAYFONTCOLOR). Each reads the control's
//     pixels (PrintWindow of the host with PW_RENDERFULLCONTENT, else of the
//     control, else a screen BitBlt; the first run's PrintWindow of the
//     child alone read nothing) and counts the emoji's yellow pixels, the
//     red B (the read sees colour) and the one-colour ☺ (ClearType's
//     fringes). A child process (-d2d-child) also sends EM_SWITCHTOD2D, an
//     Office RichEdit message, to a fresh control. See emoji_windows.go for
//     why a windowed msftedit control is expected to draw emoji in one
//     colour;
//   - IME: the messages alone (WM_IME_STARTCOMPOSITION, WM_IME_COMPOSITION
//     with GCS_RESULTSTR, WM_IME_ENDCOMPOSITION carry no text without an
//     input method composing; WM_IME_CHAR of U+3042), the keyboard layouts
//     that are input methods, and a real IMM composition: Microsoft's
//     Japanese IME loaded (00000411), opened on the control's context, a
//     composition string set (ImmSetCompositionStringW) and completed
//     (ImmNotifyIME), with an explicit answer;
//   - undo: EM_GETOLEINTERFACE → IRichEditOle → ITextDocument; with
//     BeginEditCollection/EndEditCollection around two EM_REPLACESELs, does
//     one EM_UNDO revert both? The same without the collection, for a
//     baseline;
//   - heights: ENM_REQUESTRESIZE, then EM_REQUESTRESIZE with 1, 3 and 10
//     lines; the heights requested through EN_REQUESTRESIZE (WM_NOTIFY);
//   - caret: EM_POSFROMCHAR at the end of the text and at the start of the
//     second line (the line height);
//   - spelling: EM_SETLANGOPTIONS with IMF_SPELLCHECKING and the options read
//     back;
//   - OLE callback: an IRichEditOleCallback made with internal/com's object
//     table and set with EM_SETOLECALLBACK; text put on the clipboard as
//     CF_UNICODETEXT and WM_PASTE sent: was QueryAcceptData called, with
//     which format, and did the text arrive?
//
// # Running it on a CI runner
//
//	go run ./cmd/probe-p5
//	go run ./cmd/probe-p5 -timeout 30s
//
// # Output
//
// One JSON object on stdout: ole_initialize (HRESULT), emoji
// (default_binding, segoe_ui_emoji, colour_typography: each with face,
// read, emoji / control_red_b / baseline_mono_symbol pixel counts,
// reader_sees_colour, colour_emoji; switch_to_d2d; colour_emoji_windowed),
// ime (messages_only, layouts, imm_composition, ime_installed, answer), undo (collection_hr, grouped_text_after_undo, grouped,
// baseline_text_after_undo), resize_heights (lines → requested height in
// pixels), caret (x, y, line_height), spellcheck (set_result,
// options_after, accepted), ole_callback (set, query_accept_calls,
// last_format, pasted_text, clipboard_ok), errors. Exit status 0 when it
// answered, 1 when it could not run (no msftedit.dll, no window, the
// timeout).
package main
