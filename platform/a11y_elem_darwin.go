//go:build darwin

package platform

import (
	"runtime"
	"unicode/utf8"

	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
)

// GNUIAXElement's methods: the NSAccessibility protocol's attributes and
// actions, answered from the node of the element's key in the last tree.

type axMethod struct {
	sel   string
	types string
	h     objc.Handler
	req   axReq // what the node needs for the method to be allowed (isAccessibilitySelectorAllowed:)
	s     objc.SEL
}

// axReq is what a node must have for an attribute or action to apply to it.
type axReq uint8

const (
	reqNone      axReq = iota
	reqText            // a text (fields, static text)
	reqRange           // a range (sliders, progress, scroll bars)
	reqRows            // a list, table or outline
	reqLevel           // a level (outline rows)
	reqIndex           // a place in a list
	reqExpand          // the expand or collapse action
	reqFocus           // the focus action
	reqSetValue        // the set-value action on a range
	reqPress           // the press action
	reqIncrement       // the increment action
	reqDecrement       // the decrement action
	reqShowMenu        // the show-menu action
)

// axElementMethods are the methods (made by axResolve: a handler reads the
// table, so it cannot be a variable's initialiser).
var axElementMethods []axMethod

func axMethodTable() []axMethod {
	return []axMethod{
		{sel: "isAccessibilityElement", types: "c@:", h: elemIsElement},
		{sel: "accessibilityRole", types: "@@:", h: elemRole},
		{sel: "accessibilitySubrole", types: "@@:", h: elemSubrole},
		{sel: "accessibilityRoleDescription", types: "@@:", h: elemRoleDescription},
		{sel: "accessibilityLabel", types: "@@:", h: elemLabel},
		{sel: "accessibilityValue", types: "@@:", h: elemValue},
		{sel: "accessibilityHelp", types: "@@:", h: elemHelp},
		{sel: "accessibilityMinValue", types: "@@:", h: elemMinValue, req: reqRange},
		{sel: "accessibilityMaxValue", types: "@@:", h: elemMaxValue, req: reqRange},
		{sel: "accessibilityParent", types: "@@:", h: elemParent},
		{sel: "accessibilityChildren", types: "@@:", h: elemChildren},
		{sel: "accessibilityWindow", types: "@@:", h: elemWindow},
		{sel: "accessibilityTopLevelUIElement", types: "@@:", h: elemWindow},
		{sel: "isAccessibilityEnabled", types: "c@:", h: elemEnabled},
		{sel: "isAccessibilitySelected", types: "c@:", h: elemSelected},
		{sel: "isAccessibilityExpanded", types: "c@:", h: elemExpanded},
		{sel: "isAccessibilityDisclosed", types: "c@:", h: elemExpanded, req: reqLevel},
		{sel: "isAccessibilityFocused", types: "c@:", h: elemFocused},
		{sel: "accessibilityDisclosureLevel", types: "q@:", h: elemLevel, req: reqLevel},
		{sel: "accessibilityIndex", types: "q@:", h: elemIndex, req: reqIndex},
		{sel: "accessibilityRows", types: "@@:", h: elemRows, req: reqRows},
		{sel: "accessibilitySelectedRows", types: "@@:", h: elemSelectedRows, req: reqRows},
		{sel: "accessibilitySelectedChildren", types: "@@:", h: elemSelectedRows, req: reqRows},
		{sel: "accessibilityHitTest:", types: "@@:{CGPoint=dd}", h: elemHitTest},
		{sel: "isAccessibilitySelectorAllowed:", types: "c@::", h: elemSelectorAllowed},
		{sel: "setAccessibilityFocused:", types: "v@:c", h: elemSetFocused, req: reqFocus},
		{sel: "setAccessibilityExpanded:", types: "v@:c", h: elemSetExpanded, req: reqExpand},
		{sel: "setAccessibilityDisclosed:", types: "v@:c", h: elemSetExpanded, req: reqExpand},
		{sel: "setAccessibilityValue:", types: "v@:@", h: elemSetValue, req: reqSetValue},
		{sel: "accessibilityPerformPress", types: "c@:", h: elemPress, req: reqPress},
		{sel: "accessibilityPerformIncrement", types: "c@:", h: elemIncrement, req: reqIncrement},
		{sel: "accessibilityPerformDecrement", types: "c@:", h: elemDecrement, req: reqDecrement},
		{sel: "accessibilityPerformShowMenu", types: "c@:", h: elemShowMenu, req: reqShowMenu},
		{sel: "accessibilityActionNames", types: "@@:", h: elemActionNames},
		{sel: "accessibilityPerformAction:", types: "v@:@", h: elemPerformAction},
		{sel: "accessibilityActionDescription:", types: "@@:@", h: elemActionDescription},
		{sel: "accessibilityNumberOfCharacters", types: "q@:", h: elemNumberOfCharacters, req: reqText},
		{sel: "accessibilitySelectedText", types: "@@:", h: elemSelectedText, req: reqText},
		{sel: "accessibilitySelectedTextRange", types: "{_NSRange=QQ}@:", h: elemSelectedTextRange, req: reqText},
		{sel: "accessibilityVisibleCharacterRange", types: "{_NSRange=QQ}@:", h: elemVisibleRange, req: reqText},
		{sel: "accessibilityInsertionPointLineNumber", types: "q@:", h: elemInsertionLine, req: reqText},
		{sel: "accessibilityLineForIndex:", types: "q@:q", h: elemLineForIndex, req: reqText},
		{sel: "accessibilityRangeForLine:", types: "{_NSRange=QQ}@:q", h: elemRangeForLine, req: reqText},
		{sel: "accessibilityStringForRange:", types: "@@:{_NSRange=QQ}", h: elemStringForRange, req: reqText},
		{sel: "accessibilityAttributedStringForRange:", types: "@@:{_NSRange=QQ}", h: elemAttributedStringForRange, req: reqText},
		{sel: "accessibilityRangeForPosition:", types: "{_NSRange=QQ}@:{CGPoint=dd}", h: elemRangeForPosition, req: reqText},
		{sel: "accessibilityRangeForIndex:", types: "{_NSRange=QQ}@:q", h: elemRangeForIndex, req: reqText},
		{sel: "accessibilityStyleRangeForIndex:", types: "{_NSRange=QQ}@:q", h: elemRangeForIndex, req: reqText},
	}
}

// selAXFrameForRange is the selector of the frame of a range, which is
// allowed for text only.
var selAXFrameForRange objc.SEL

// axResolve fills in the table's selectors (axSetup, main thread): the table
// cannot have them in its initialiser, since a handler in it reads it.
func axResolve() {
	axElementMethods = axMethodTable()
	for i := range axElementMethods {
		axElementMethods[i].s = objc.Sel(axElementMethods[i].sel)
	}
	selAXFrameForRange = objc.Sel("accessibilityFrameForRange:")
}

// meets reports whether node n meets requirement r.
func (r axReq) meets(t *AXTree, n *AXNode, i int32) bool {
	switch r {
	case reqText:
		return n.Text != AXTextNone || axValueOf(n) == valueString
	case reqRange:
		k := axValueOf(n)
		return k == valueRange || k == valueScroll
	case reqRows:
		return n.Role == AXRoleList || n.Role == AXRoleTable
	case reqLevel:
		return n.Level > 0 && n.Role != AXRoleHeading
	case reqIndex:
		return n.Pos[1] > 0
	case reqExpand:
		return n.Actions&(AXExpand|AXCollapse) != 0
	case reqFocus:
		return n.Actions&AXFocus != 0
	case reqSetValue:
		return n.Actions&AXSetValue != 0 && (axValueOf(n) == valueRange || axValueOf(n) == valueScroll)
	case reqPress:
		return n.Actions&AXPress != 0
	case reqIncrement:
		return n.Actions&AXIncrement != 0
	case reqDecrement:
		return n.Actions&AXDecrement != 0
	case reqShowMenu:
		return n.Actions&AXShowMenu != 0
	}
	return true
}

func self(c *ffi.CB) objc.ID { return objc.ID(c.Int(0)) }

func retObj(c *ffi.CB, o objc.ID) { c.Ret(uintptr(o)) }

func elemIsElement(c *ffi.CB) {
	_, _, ok := elemNode(self(c))
	c.RetBool(ok)
}

func elemRole(c *ffi.CB) {
	_, i, ok := elemNode(self(c))
	if !ok {
		c.Ret(0)
		return
	}
	r, _ := axRoleOf(ax.core.tree(), i)
	retObj(c, axk.names[r])
}

func elemSubrole(c *ffi.CB) {
	_, i, ok := elemNode(self(c))
	if !ok {
		c.Ret(0)
		return
	}
	_, s := axRoleOf(ax.core.tree(), i)
	retObj(c, axk.names[s])
}

func elemRoleDescription(c *ffi.CB) {
	_, i, ok := elemNode(self(c))
	if !ok || axk.roleDesc == 0 {
		c.Ret(0)
		return
	}
	r, s := axRoleOf(ax.core.tree(), i)
	f := c.Frame()
	c.Ret(f.Reset().Arg(uintptr(axk.names[r])).Arg(uintptr(axk.names[s])).Call(axk.roleDesc).R0())
}

func elemLabel(c *ffi.CB) {
	n, _, ok := elemNode(self(c))
	if !ok {
		c.Ret(0)
		return
	}
	l := axLabel(ax.core.tree(), n)
	if len(l) == 0 {
		c.Ret(0)
		return
	}
	retObj(c, axStr(l))
}

func elemValue(c *ffi.CB) {
	n, _, ok := elemNode(self(c))
	if !ok {
		c.Ret(0)
		return
	}
	switch axValueOf(n) {
	case valueString:
		retObj(c, axStr(axTextFor(n)))
	case valueToggle, valueRange, valueScroll:
		retObj(c, nsNumber(axNumber(n)))
	default:
		c.Ret(0)
	}
}

func elemHelp(c *ffi.CB) {
	n, _, ok := elemNode(self(c))
	if !ok || ax.src == nil {
		c.Ret(0)
		return
	}
	ax.tmp = ax.src.AXHelp(n.Key, ax.tmp[:0])
	if len(ax.tmp) == 0 {
		c.Ret(0)
		return
	}
	retObj(c, axStr(ax.tmp))
}

func elemMinValue(c *ffi.CB) { elemBound(c, 0) }
func elemMaxValue(c *ffi.CB) { elemBound(c, 1) }

func elemBound(c *ffi.CB, k int) {
	n, _, ok := elemNode(self(c))
	if !ok || !reqRange.meets(nil, n, 0) {
		c.Ret(0)
		return
	}
	if axValueOf(n) == valueScroll {
		retObj(c, nsNumber(float64(k)))
		return
	}
	retObj(c, nsNumber(n.Range[k]))
}

func elemParent(c *ffi.CB) {
	n, _, ok := elemNode(self(c))
	if !ok {
		c.Ret(0)
		return
	}
	p := n.Parent
	if p < 0 {
		p = 0
	}
	retObj(c, ax.objOf(p))
}

func elemChildren(c *ffi.CB) {
	_, i, ok := elemNode(self(c))
	if !ok {
		retObj(c, objc.Send(clsNSArray, selArray))
		return
	}
	retObj(c, ax.children(i))
}

func elemWindow(c *ffi.CB) {
	if _, _, ok := elemNode(self(c)); !ok {
		c.Ret(0)
		return
	}
	retObj(c, ax.w.win)
}

func elemState(c *ffi.CB, bit AXState, want bool) {
	n, _, ok := elemNode(self(c))
	c.RetBool(ok && (n.State&bit != 0) == want)
}

func elemEnabled(c *ffi.CB)  { elemState(c, AXDisabled, false) }
func elemSelected(c *ffi.CB) { elemState(c, AXSelected, true) }
func elemExpanded(c *ffi.CB) { elemState(c, AXExpanded, true) }
func elemFocused(c *ffi.CB)  { elemState(c, AXFocused, true) }

func elemLevel(c *ffi.CB) {
	n, _, ok := elemNode(self(c))
	if !ok || n.Level <= 0 {
		c.Ret(0)
		return
	}
	c.Ret(uintptr(n.Level - 1))
}

func elemIndex(c *ffi.CB) {
	n, _, ok := elemNode(self(c))
	if !ok {
		c.Ret(0)
		return
	}
	c.Ret(uintptr(n.Pos[0]))
}

// rows appends the rows of list or table node i: its children and its scroll
// area's or group's children that are rows.
func axRows(t *AXTree, i int32, dst []int32, selectedOnly bool) []int32 {
	add := func(ch int32) {
		n := &t.Nodes[ch]
		if (n.Role == AXRoleRow || n.Role == AXRoleListItem) && (!selectedOnly || n.State&AXSelected != 0) {
			dst = append(dst, ch)
		}
	}
	for ch := t.Nodes[i].FirstChild; ch >= 0; ch = t.Nodes[ch].NextSibling {
		add(ch)
		if r := t.Nodes[ch].Role; r == AXRoleScrollArea || r == AXRoleGroup {
			for g := t.Nodes[ch].FirstChild; g >= 0; g = t.Nodes[g].NextSibling {
				add(g)
			}
		}
	}
	return dst
}

func elemRows(c *ffi.CB)         { elemRowList(c, false) }
func elemSelectedRows(c *ffi.CB) { elemRowList(c, true) }

func elemRowList(c *ffi.CB, sel bool) {
	n, i, ok := elemNode(self(c))
	if !ok || !reqRows.meets(nil, n, i) {
		c.Ret(0)
		return
	}
	ax.nodes = axRows(ax.core.tree(), i, ax.nodes[:0], sel)
	ax.kids = ax.kids[:0]
	for _, r := range ax.nodes {
		if o := ax.objOf(r); o != 0 {
			ax.kids = append(ax.kids, uintptr(o))
		}
	}
	retObj(c, axArray(ax.kids))
}

func elemHitTest(c *ffi.CB) {
	if _, _, ok := elemNode(self(c)); !ok {
		c.Ret(0)
		return
	}
	retObj(c, ax.hit(c.F64(0), c.F64(1)))
}

func elemSelectorAllowed(c *ffi.CB) {
	n, i, ok := elemNode(self(c))
	if !ok {
		c.RetBool(false)
		return
	}
	sel := objc.SEL(c.Int(2))
	if sel == selAXFrameForRange {
		c.RetBool(reqText.meets(ax.core.tree(), n, i))
		return
	}
	for k := range axElementMethods {
		if m := &axElementMethods[k]; m.s == sel {
			c.RetBool(m.req.meets(ax.core.tree(), n, i))
			return
		}
	}
	// Setters this class does not implement (NSAccessibilityElement's
	// stored properties) are not the app's: they are refused.
	name := objc.SelName(sel)
	c.RetBool(len(name) < 3 || name[:3] != "set")
}

func elemAct(c *ffi.CB, a AXAction, v float64) bool {
	n, _, ok := elemNode(self(c))
	return ok && ax.action(n, a, v)
}

func elemPress(c *ffi.CB)     { c.RetBool(elemAct(c, AXPress, 0)) }
func elemIncrement(c *ffi.CB) { c.RetBool(elemAct(c, AXIncrement, 0)) }
func elemDecrement(c *ffi.CB) { c.RetBool(elemAct(c, AXDecrement, 0)) }
func elemShowMenu(c *ffi.CB)  { c.RetBool(elemAct(c, AXShowMenu, 0)) }

func elemSetFocused(c *ffi.CB) {
	if byte(c.Int(2)) != 0 {
		elemAct(c, AXFocus, 0)
	}
}

func elemSetExpanded(c *ffi.CB) {
	if byte(c.Int(2)) != 0 {
		elemAct(c, AXExpand, 0)
	} else {
		elemAct(c, AXCollapse, 0)
	}
}

func elemSetValue(c *ffi.CB) {
	n, _, ok := elemNode(self(c))
	v := objc.ID(c.Int(2))
	if !ok || v == 0 || !reqSetValue.meets(nil, n, 0) || !objc.SendBool1(v, selRespondsTo, uintptr(selDoubleValue)) {
		return
	}
	f := objc.F.Reset().Arg(uintptr(v)).Arg(uintptr(selDoubleValue)).Call(objc.MsgSend).F64(0)
	if axValueOf(n) == valueScroll {
		f = n.Range[0] + f*(n.Range[1]-n.Range[0])
	}
	ax.action(n, AXSetValue, f)
}

func elemActionNames(c *ffi.CB) {
	n, _, ok := elemNode(self(c))
	ax.kids = ax.kids[:0]
	if ok && n.State&AXDisabled == 0 {
		for k, a := range axActionNames {
			if n.Actions&a.act != 0 {
				ax.kids = append(ax.kids, uintptr(axk.actions[k]))
			}
		}
	}
	retObj(c, axArray(ax.kids))
}

// axActionOf is the action named by NSString name, 0 for one not offered.
func axActionOf(name objc.ID) AXAction {
	if name == 0 {
		return 0
	}
	for k, a := range axActionNames {
		if objc.SendBool1(name, selIsEqualToString, uintptr(axk.actions[k])) {
			return a.act
		}
	}
	return 0
}

func elemPerformAction(c *ffi.CB) {
	if a := axActionOf(objc.ID(c.Int(2))); a != 0 {
		elemAct(c, a, 0)
	}
}

var axActionDescriptions = [len(axActionNames)]string{"press", "increment", "decrement", "show menu", "scroll to visible"}

func elemActionDescription(c *ffi.CB) {
	a := axActionOf(objc.ID(c.Int(2)))
	for k, x := range axActionNames {
		if x.act == a && a != 0 {
			retObj(c, axStr([]byte(axActionDescriptions[k])))
			return
		}
	}
	c.Ret(0)
}

// ---- text ----

// axTextFor is the text of node n the text attributes are about: the text
// with geometry, or the string value.
func axTextFor(n *AXNode) []byte {
	t := ax.core.tree()
	if n.Text != AXTextNone {
		return t.text(n)
	}
	return axString(t, n)
}

// elemText is element self's node and text, when it has one.
func elemText(c *ffi.CB) (*AXNode, []byte, bool) {
	n, _, ok := elemNode(self(c))
	if !ok || !reqText.meets(nil, n, 0) {
		return nil, nil, false
	}
	return n, axTextFor(n), true
}

// unitRange converts the byte range [lo, hi) of b to UTF-16 units.
func unitRange(b []byte, lo, hi int) (loc, n uintptr) {
	lo, hi = min(max(lo, 0), len(b)), min(max(hi, 0), len(b))
	if hi < lo {
		lo, hi = hi, lo
	}
	return uintptr(utf16Count(b[:lo])), uintptr(utf16Count(b[lo:hi]))
}

// byteRange converts the UTF-16 range {loc, n} of b to bytes.
func byteRange(b []byte, loc, n uintptr) (lo, hi int) {
	lo = byteOfUnit(b, int(min(loc, 1<<31)))
	hi = byteOfUnit(b, int(min(loc+min(n, 1<<31), 1<<31)))
	return lo, max(hi, lo)
}

func elemNumberOfCharacters(c *ffi.CB) {
	_, txt, ok := elemText(c)
	if !ok {
		c.Ret(0)
		return
	}
	c.Ret(uintptr(utf16Count(txt)))
}

// sel is node n's selection in bytes of txt, low end first.
func textSel(n *AXNode, txt []byte) (lo, hi int) {
	a, b := int(n.TextSel[0]), int(n.TextSel[1])
	if a > b {
		a, b = b, a
	}
	return min(max(a, 0), len(txt)), min(max(b, 0), len(txt))
}

func elemSelectedText(c *ffi.CB) {
	n, txt, ok := elemText(c)
	if !ok {
		c.Ret(0)
		return
	}
	lo, hi := textSel(n, txt)
	retObj(c, axStr(txt[lo:hi]))
}

func elemSelectedTextRange(c *ffi.CB) {
	n, txt, ok := elemText(c)
	if !ok {
		c.Ret2(0, 0)
		return
	}
	lo, hi := textSel(n, txt)
	c.Ret2(unitRange(txt, lo, hi))
}

func elemVisibleRange(c *ffi.CB) {
	_, txt, ok := elemText(c)
	if !ok {
		c.Ret2(0, 0)
		return
	}
	c.Ret2(0, uintptr(utf16Count(txt)))
}

// lineOf is the line of byte offset off of node n's text.
func lineOf(n *AXNode, off int) int {
	if n.Text == AXTextNone || ax.src == nil {
		return 0
	}
	l, ok := ax.src.AXLine(n.Key, off)
	if !ok {
		return 0
	}
	return l
}

func elemInsertionLine(c *ffi.CB) {
	n, txt, ok := elemText(c)
	if !ok {
		c.Ret(0)
		return
	}
	caret := min(max(int(n.TextSel[1]), 0), len(txt))
	c.Ret(uintptr(lineOf(n, caret)))
}

func elemLineForIndex(c *ffi.CB) {
	n, txt, ok := elemText(c)
	if !ok {
		c.Ret(0)
		return
	}
	c.Ret(uintptr(lineOf(n, byteOfUnit(txt, int(min(c.Int(2), 1<<31))))))
}

func elemRangeForLine(c *ffi.CB) {
	n, txt, ok := elemText(c)
	if !ok {
		c.Ret2(nsNotFound, 0)
		return
	}
	line := int(min(c.Int(2), 1<<31))
	if n.Text == AXTextNone || ax.src == nil {
		if line != 0 {
			c.Ret2(nsNotFound, 0)
			return
		}
		c.Ret2(0, uintptr(utf16Count(txt)))
		return
	}
	lo, hi, ok := ax.src.AXLineRange(n.Key, line)
	if !ok {
		c.Ret2(nsNotFound, 0)
		return
	}
	c.Ret2(unitRange(txt, lo, hi))
}

func elemStringForRange(c *ffi.CB) {
	_, txt, ok := elemText(c)
	if !ok {
		c.Ret(0)
		return
	}
	lo, hi := byteRange(txt, c.Int(2), c.Int(3))
	retObj(c, axStr(txt[lo:hi]))
}

func elemAttributedStringForRange(c *ffi.CB) {
	_, txt, ok := elemText(c)
	if !ok {
		c.Ret(0)
		return
	}
	lo, hi := byteRange(txt, c.Int(2), c.Int(3))
	s := axStr(txt[lo:hi])
	a := objc.Send1(objc.Send(axk.clsAttr, selAlloc), selInitWithString, uintptr(s))
	retObj(c, objc.Send(a, selAutorelease))
}

func elemRangeForPosition(c *ffi.CB) {
	n, txt, ok := elemText(c)
	if !ok || n.Text == AXTextNone || ax.src == nil {
		c.Ret2(nsNotFound, 0)
		return
	}
	px, py := ax.w.axToPixels(c.F64(0), c.F64(1))
	off, ok := ax.src.AXOffset(n.Key, px, py)
	if !ok {
		c.Ret2(nsNotFound, 0)
		return
	}
	c.Ret2(charRange(txt, off))
}

// charRange is the UTF-16 range of the character at byte offset off of b.
func charRange(b []byte, off int) (loc, n uintptr) {
	off = min(max(off, 0), len(b))
	_, size := utf8.DecodeRune(b[off:])
	return unitRange(b, off, off+size)
}

func elemRangeForIndex(c *ffi.CB) {
	_, txt, ok := elemText(c)
	if !ok {
		c.Ret2(nsNotFound, 0)
		return
	}
	c.Ret2(charRange(txt, byteOfUnit(txt, int(min(c.Int(2), 1<<31)))))
}

// ---- frames ----

// retRect returns r from a CGRect-returning IMP (see firstRectIMP).
func retRect(c *ffi.CB, r objc.Rect) {
	if runtime.GOARCH == "amd64" {
		*(*objc.Rect)(c.IndirectResult()) = r
		return
	}
	c.RetF64(0, r.X)
	c.RetF64(1, r.Y)
	c.RetF64(2, r.W)
	c.RetF64(3, r.H)
}

// imp args: on amd64 the hidden result pointer comes first.
func impArg(c *ffi.CB, i int) uintptr {
	if runtime.GOARCH == "amd64" {
		return c.Int(i + 1)
	}
	return c.Int(i)
}

func elemFrameIMP(c *ffi.CB) {
	var r objc.Rect
	if n, _, ok := elemNode(objc.ID(impArg(c, 0))); ok {
		r = ax.w.axScreenRect(n.Bounds)
	}
	retRect(c, r)
}

func elemFrameForRangeIMP(c *ffi.CB) {
	var r objc.Rect
	if n, _, ok := elemNode(objc.ID(impArg(c, 0))); ok && reqText.meets(nil, n, 0) {
		txt := axTextFor(n)
		lo, hi := byteRange(txt, impArg(c, 2), impArg(c, 3))
		b := n.Bounds
		if n.Text != AXTextNone && ax.src != nil {
			if q, ok := ax.src.AXBounds(n.Key, lo, hi); ok {
				b = q
			}
		}
		r = ax.w.axScreenRect(b)
	}
	retRect(c, r)
}
