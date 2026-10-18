package rex

// The snake game's colours, 0xRRGGBB: dimmed (as in the reference) and focused
// in the light mode, then the same pair in the dark one.
var (
	scTeal = [4]uint32{0x67A3B3, 0x218A91, 0x5FA9AD, 0x5FD0D6}
	scFood = [4]uint32{0xBC5263, 0xCF3E4B, 0xE17F88, 0xFF8791}
	scBody = [4]uint32{0x558E65, 0x378925, 0x5E9E6A, 0x6FCF7C}
	scHead = [4]uint32{0x5BA26E, 0x2F8F4C, 0x6FBF82, 0x86DB8E}
)
