package rexui

import (
	"reflect"

	"gonativeui/menu"
	"gonativeui/widget/base"
)

// Strings are the words the components show, or give assistive technologies,
// where the app passes none: a code block's Copy button, the names of the
// sidebar's disclosure and a sent message, a banner's level. English is the
// default and StringsPtBR is Brazilian Portuguese; an app in another language
// fills its own, and SetStrings keeps English in the fields it leaves empty.
// Whatever the app passes (a label, BubbleOpts.Name) wins over these.
type Strings struct {
	// Base are the primitives' names (scroll bars, dividers).
	Base base.Strings
	// Menu are the titles of the standard commands and menus (Copy, Quit,
	// Edit, Window), drawn, in the tray and native (menu.SetStrings).
	Menu menu.Strings

	// Copy is a code block's copy button; Code names the block.
	Copy, Code string
	// ShowOrHide names a sidebar section's disclosure; Disclosure, a row's.
	ShowOrHide, Disclosure string
	// BannerInfo … BannerError begin a banner's name ("Warning: title");
	// Dismiss names its close button.
	BannerInfo, BannerSuccess, BannerWarning, BannerError, Dismiss string
	// Message and SentMessage name an incoming and an outgoing bubble with no
	// name of their own; Sending and NotDelivered are a bubble's status (and
	// Sending the composer's send button while a send is in flight).
	Message, SentMessage, Sending, NotDelivered string
	// Online, Away and Busy are an avatar's presence.
	Online, Away, Busy string
	// Typing replaces a chat row's preview while the contact types.
	Typing string
	// Colour names the colour well's picker and Hex its field; Colors are its
	// swatches: red, orange, yellow, green, mint, teal, cyan, blue, indigo,
	// purple, pink, brown.
	Colour, Hex string
	Colors      [12]string
	// ShowOptions names a combo box's arrow; Options, its list.
	ShowOptions, Options string
	// MessageField names the composer's field, Attachments its row of
	// attachments and Suggestions its completion list (unless
	// ComposerOpts.CompletionLabel names it); Remove, followed by a space and
	// the attachment's name, names a chip's remove button; Send, the send
	// button.
	MessageField, Attachments, Suggestions, Remove, Send string
	// Task names a markdown task-list checkbox.
	Task string
	// CloseTab names a tab's close button; ShowTab (followed by a space and
	// the number) and ShowLastTab title TabCommands.
	CloseTab, ShowTab, ShowLastTab string
	// Clear names a search field's clear button.
	Clear string
	// Back, Forward and BackForward name the toolbar's navigation (and Back
	// and Forward its entries in the overflow menu); ShowMore, the overflow
	// button.
	Back, Forward, BackForward, ShowMore string
}

// StringsEnglish are the default Strings.
var StringsEnglish = Strings{
	Base:          base.StringsEnglish,
	Menu:          menu.StringsEnglish,
	Copy:          "Copy",
	Code:          "Code",
	ShowOrHide:    "Show or Hide",
	Disclosure:    "Disclosure",
	BannerInfo:    "Info",
	BannerSuccess: "Success",
	BannerWarning: "Warning",
	BannerError:   "Error",
	Dismiss:       "Dismiss",
	Message:       "Message",
	SentMessage:   "Sent message",
	Sending:       "Sending",
	NotDelivered:  "Not delivered",
	Online:        "Online",
	Away:          "Away",
	Busy:          "Busy",
	Typing:        "typing…",
	Colour:        "Colour",
	Hex:           "Hex",
	Colors:        [12]string{"Red", "Orange", "Yellow", "Green", "Mint", "Teal", "Cyan", "Blue", "Indigo", "Purple", "Pink", "Brown"},
	ShowOptions:   "Show options",
	Options:       "Options",
	MessageField:  "Message",
	Attachments:   "Attachments",
	Suggestions:   "Suggestions",
	Remove:        "Remove",
	Send:          "Send",
	Task:          "Task",
	CloseTab:      "Close Tab",
	ShowTab:       "Show Tab",
	ShowLastTab:   "Show Last Tab",
	Clear:         "Clear",
	Back:          "Back",
	Forward:       "Forward",
	BackForward:   "Back/Forward",
	ShowMore:      "Show More",
}

// StringsPtBR are the Strings in Brazilian Portuguese, in macOS's words.
var StringsPtBR = Strings{
	Base: base.Strings{
		HorizontalScrollBar: "Barra de rolagem horizontal",
		VerticalScrollBar:   "Barra de rolagem vertical",
		Divider:             "Divisor",
	},
	// macOS 27's own: AppKit's MenuCommands, Common and Services tables and
	// the stock apps' main menus (Dicionário, Xadrez).
	Menu: menu.Strings{
		About: "Sobre", Hide: "Ocultar", Quit: "Encerrar",
		AboutApp: "Sobre o %s", HideApp: "Ocultar %s", QuitApp: "Encerrar %s",
		HideOthers: "Ocultar Outros", ShowAll: "Mostrar Tudo", Settings: "Ajustes…",
		Undo: "Desfazer", Redo: "Refazer", Cut: "Cortar", Copy: "Copiar", Paste: "Colar",
		PasteAndMatchStyle: "Colar com o Mesmo Estilo", Delete: "Apagar", SelectAll: "Selecionar Tudo",
		Minimize: "Minimizar", Zoom: "Zoom", Close: "Fechar", CloseWindow: "Fechar Janela",
		BringAllToFront: "Trazer Todas para a Frente", EnterFullScreen: "Entrar em Tela Cheia",
		Services: "Serviços", Edit: "Editar", Window: "Janela", Help: "Ajuda",
	},
	Copy:          "Copiar",
	Code:          "Código",
	ShowOrHide:    "Mostrar ou Ocultar",
	Disclosure:    "Expandir ou Recolher",
	BannerInfo:    "Informação",
	BannerSuccess: "Sucesso",
	BannerWarning: "Aviso",
	BannerError:   "Erro",
	Dismiss:       "Fechar",
	Message:       "Mensagem",
	SentMessage:   "Mensagem enviada",
	Sending:       "Enviando",
	NotDelivered:  "Não entregue",
	Online:        "Online",
	Away:          "Ausente",
	Busy:          "Ocupado",
	Typing:        "digitando…",
	Colour:        "Cor",
	Hex:           "Hex",
	Colors:        [12]string{"Vermelho", "Laranja", "Amarelo", "Verde", "Menta", "Verde-azulado", "Ciano", "Azul", "Índigo", "Roxo", "Rosa", "Marrom"},
	ShowOptions:   "Mostrar opções",
	Options:       "Opções",
	MessageField:  "Mensagem",
	Attachments:   "Anexos",
	Suggestions:   "Sugestões",
	Remove:        "Remover",
	Send:          "Enviar",
	Task:          "Tarefa",
	CloseTab:      "Fechar Aba",
	ShowTab:       "Mostrar Aba",
	ShowLastTab:   "Mostrar Última Aba",
	Clear:         "Limpar",
	Back:          "Voltar",
	Forward:       "Avançar",
	BackForward:   "Voltar/Avançar",
	ShowMore:      "Mostrar Mais",
}

var (
	// str are the Strings in use; the components read them every frame.
	str = StringsEnglish
	// sendLabels are the send button's labels (idle, sending), one identity.
	sendLabels = [2]string{"Send###send", "Sending###send"}
)

// SetStrings makes s the words of every component, s.Base those of the
// primitives (base.SetStrings) and s.Menu the standard menu titles
// (menu.SetStrings); a field left empty keeps its English. Call it before the
// first frame, or between frames on the UI goroutine (native menus take the
// titles when they are next built):
//
//	rexui.SetStrings(rexui.StringsPtBR)
func SetStrings(s Strings) {
	orEnglish(reflect.ValueOf(&s).Elem(), reflect.ValueOf(StringsEnglish))
	base.SetStrings(s.Base)
	menu.SetStrings(s.Menu)
	str = s
	sendLabels = [2]string{s.Send + "###send", s.Sending + "###send"}
}

// CurrentStrings returns the Strings in use.
func CurrentStrings() Strings { return str }

// orEnglish fills the empty strings of v, a Strings or a part of one, from
// en, the same part of StringsEnglish.
func orEnglish(v, en reflect.Value) {
	switch v.Kind() {
	case reflect.String:
		if v.Len() == 0 {
			v.SetString(en.String())
		}
	case reflect.Struct:
		for i := range v.NumField() {
			orEnglish(v.Field(i), en.Field(i))
		}
	case reflect.Array:
		for i := range v.Len() {
			orEnglish(v.Index(i), en.Index(i))
		}
	}
}
