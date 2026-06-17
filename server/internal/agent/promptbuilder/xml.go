package promptbuilder

// --- XML structs for encoding/xml.MarshalIndent ---

// --- memory ---
type xmlMemory struct {
	Topic   string `xml:"topic,attr,omitempty"`
	Date    string `xml:"date,attr,omitempty"`
	Content string `xml:",chardata"`
}
type xmlMemories struct {
	XMLName struct{}    `xml:"memories"`
	Items   []xmlMemory `xml:"memory"`
}

// --- note ---
type xmlNote struct {
	Topic   string `xml:"topic,attr,omitempty"`
	Date    string `xml:"date,attr,omitempty"`
	Content string `xml:",chardata"`
}
type xmlNotes struct {
	XMLName struct{}  `xml:"notes"`
	Items   []xmlNote `xml:"note"`
}

// --- history ---
type xmlHistoryMsg struct {
	Role    string `xml:"role,attr"`
	Content string `xml:",chardata"`
}
type xmlHistoryRound struct {
	Messages []xmlHistoryMsg `xml:"message"`
	Date     string          `xml:"date,attr,omitempty"`
}
type xmlHistory struct {
	XMLName struct{}          `xml:"history"`
	Rounds  []xmlHistoryRound `xml:"round"`
}

// --- theme ---
type xmlTheme struct {
	Description string `xml:"desc,attr,omitempty"`
	Content     string `xml:",chardata"`
}
type xmlThemes struct {
	XMLName struct{}   `xml:"topics"`
	Items   []xmlTheme `xml:"topic"`
}
