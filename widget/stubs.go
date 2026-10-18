package widget

type popup struct{}

func (p *popup) reset() {}

type tipState struct{}

func (k *Kit) tipBegin()    {}
func (k *Kit) contextMenu() {}

type glyphs struct{ k *Kit }
