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
//   - emoji: sets "A😀B" at 24 pt (WM_SETTEXT, EM_SETCHARFORMAT), reads the
//     emoji's font back (EM_GETCHARFORMAT with CHARFORMAT2W: font binding
//     should pick Segoe UI Emoji), asks for advanced typography
//     (EM_SETTYPOGRAPHYOPTIONS) and reads EM_GETEDITSTYLEEX, and counts
//     coloured (non-grey) pixels in the emoji's cell from PrintWindow. No
//     public EM_SETEDITSTYLEEX flag is known to switch a windowed RichEdit to
//     Direct2D rendering, so the pixel count is the answer to "colour emoji";
//   - IME: ImmGetContext on the control, then WM_IME_STARTCOMPOSITION,
//     WM_IME_COMPOSITION (GCS_RESULTSTR), WM_IME_ENDCOMPOSITION and a
//     WM_IME_CHAR of U+3042 sent to it, with the text before and after;
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
// One JSON object on stdout: ole_initialize (HRESULT), richedit_version
// (msftedit.dll found), emoji (face, charset, typography_accepted,
// edit_style_ex, coloured_pixels, cell), ime (context, the text after each
// message), undo (collection_hr, grouped_text_after_undo, grouped,
// baseline_text_after_undo), resize_heights (lines → requested height in
// pixels), caret (x, y, line_height), spellcheck (set_result,
// options_after, accepted), ole_callback (set, query_accept_calls,
// last_format, pasted_text, clipboard_ok), errors. Exit status 0 when it
// answered, 1 when it could not run (no msftedit.dll, no window, the
// timeout).
package main
