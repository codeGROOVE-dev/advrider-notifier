package email

import (
	"strings"
	"testing"
)

// TestSanitizeHTMLWithRealADVRiderPost tests that our sanitizer correctly handles
// actual HTML from ADVRider forum posts without breaking formatting, images, or links.
// This test uses real HTML structure from post #53733501 in the "Fin and Mechanico Spank the World - France" thread.
func TestSanitizeHTMLWithRealADVRiderPost(t *testing.T) {
	// This is actual HTML extracted from ADVRider post #53733501
	input := `<b>France</b><br />
<br />
I spent a full day in the small ski town of <a href="https://maps.app.goo.gl/VMGyg7XW4QZpFExZ6" target="_blank" class="externalLink" rel="nofollow"><span style="font-size: 15px">Le Grand-Bornand</span></a>. I also live in a ski town so I understand that this is their down season. The place was VERY quiet and about 90% of the businesses were closed. I could only find one restaurant and one food market open. The chalet I was in probably had 12 units in it. I think I was the only one there.<br />
<br />


	<img src="https://advrider.com/f/attachments/advrider-2025_10_12-1-jpg.7308191/" alt="ADVRider 2025_10_12 (1).jpg" class="bbCodeImage LbImage" />

<br />
<br />


	<img src="https://advrider.com/f/attachments/advrider-2025_10_12-2-jpg.7308193/" alt="ADVRider 2025_10_12 (2).jpg" class="bbCodeImage LbImage" />

<br />
<br />
And I am in France. The wine selection was excellent, but they had a very few options for beer. So I stuck to old faithful and brought a little bit of Mexico to the French Alps.<br />
<br />


	<img src="https://advrider.com/f/attachments/advrider-2025_10_12-4-jpg.7308197/" alt="ADVRider 2025_10_12 (4).jpg" class="bbCodeImage LbImage" />

<br />
<br />
As always, following the Ad&#039;T as it takes me through lots more ski towns in sleep mode until the snow arrives.`

	result := sanitizeHTML(input)

	// Test 1: Bold tag should be preserved
	if !strings.Contains(result, "<b>France</b>") {
		t.Error("Bold tag should be preserved")
	}

	// Test 2: Line breaks should be preserved
	if !strings.Contains(result, "<br>") {
		t.Error("Line breaks should be preserved")
	}

	// Test 3: Images should be preserved with src and alt attributes
	if !strings.Contains(result, `<img src="https://advrider.com/f/attachments/advrider-2025_10_12-1-jpg.7308191/"`) {
		t.Error("Image src attribute should be preserved")
	}
	if !strings.Contains(result, `alt="ADVRider 2025_10_12 (1).jpg"`) {
		t.Error("Image alt attribute should be preserved")
	}

	// Test 4: Links should be preserved with href attribute
	if !strings.Contains(result, `<a href="https://maps.app.goo.gl/VMGyg7XW4QZpFExZ6">`) {
		t.Error("Link href attribute should be preserved")
	}

	// Test 5: Dangerous attributes should be stripped (target, class, rel, style)
	if strings.Contains(result, `target="_blank"`) {
		t.Error("target attribute should be stripped from links")
	}
	if strings.Contains(result, `class="externalLink"`) {
		t.Error("class attribute should be stripped from links")
	}
	if strings.Contains(result, `rel="nofollow"`) {
		t.Error("rel attribute should be stripped from links")
	}
	if strings.Contains(result, `style="font-size: 15px"`) {
		t.Error("style attribute should be stripped from spans")
	}
	if strings.Contains(result, `class="bbCodeImage LbImage"`) {
		t.Error("class attribute should be stripped from images")
	}

	// Test 6: Text content should be preserved
	if !strings.Contains(result, "I spent a full day in the small ski town of") {
		t.Error("Text content should be preserved")
	}
	if !strings.Contains(result, "Le Grand-Bornand") {
		t.Error("Link text should be preserved")
	}

	// Test 7: HTML entities should be preserved
	if !strings.Contains(result, "Ad&#039;T") {
		t.Error("HTML entities should be preserved")
	}

	// Test 8: Span tags should be preserved (they're in our whitelist)
	if !strings.Contains(result, "<span>") {
		t.Error("Span tags should be preserved")
	}
	if !strings.Contains(result, "</span>") {
		t.Error("Closing span tags should be preserved")
	}

	// Test 9: Verify no script tags could sneak through
	maliciousInput := `<script>alert('xss')</script>`
	maliciousResult := sanitizeHTML(maliciousInput)
	if strings.Contains(maliciousResult, "<script>") {
		t.Error("Script tags should be escaped, not preserved")
	}
	if !strings.Contains(maliciousResult, "&lt;script&gt;") {
		t.Error("Script tags should be escaped as HTML entities")
	}
}

// TestSanitizeHTMLBlockquotes tests that blockquotes (used for quotes in posts) are preserved.
func TestSanitizeHTMLBlockquotes(t *testing.T) {
	input := `<blockquote>This is a quoted post</blockquote>`
	result := sanitizeHTML(input)

	if !strings.Contains(result, "<blockquote>") {
		t.Error("Blockquote opening tag should be preserved")
	}
	if !strings.Contains(result, "</blockquote>") {
		t.Error("Blockquote closing tag should be preserved")
	}
	if !strings.Contains(result, "This is a quoted post") {
		t.Error("Blockquote content should be preserved")
	}
}

// TestSanitizeHTMLSelfClosingTags tests that self-closing tags like <br/> and <hr/> are preserved.
func TestSanitizeHTMLSelfClosingTags(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		contains string
	}{
		{"br with slash", "<br/>", "<br>"},
		{"br with space and slash", "<br />", "<br>"},
		{"br plain", "<br>", "<br>"},
		{"hr with slash", "<hr/>", "<hr>"},
		{"hr with space and slash", "<hr />", "<hr>"},
		{"hr plain", "<hr>", "<hr>"},
		{"multiple br tags", "Line 1<br/>Line 2<br />Line 3", "<br>"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeHTML(tt.input)
			if !strings.Contains(result, tt.contains) {
				t.Errorf("Expected %q to contain %q, got: %q", tt.input, tt.contains, result)
			}
			// Ensure tags are NOT escaped
			if strings.Contains(result, "&lt;br") || strings.Contains(result, "&lt;hr") {
				t.Errorf("Tags should not be escaped, got: %q", result)
			}
		})
	}
}

// TestSanitizeHTMLLists tests that lists (ul, ol, li) are preserved.
func TestSanitizeHTMLLists(t *testing.T) {
	input := `<ul><li>First item</li><li>Second item</li></ul><ol><li>Numbered</li></ol>`
	result := sanitizeHTML(input)

	if !strings.Contains(result, "<ul>") {
		t.Error("Unordered list tag should be preserved")
	}
	if !strings.Contains(result, "<li>") {
		t.Error("List item tag should be preserved")
	}
	if !strings.Contains(result, "<ol>") {
		t.Error("Ordered list tag should be preserved")
	}
	if !strings.Contains(result, "First item") && !strings.Contains(result, "Second item") {
		t.Error("List content should be preserved")
	}
}

// TestSanitizeHTMLFormatting tests that basic formatting tags are preserved.
func TestSanitizeHTMLFormatting(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		contains string
	}{
		{"bold", "<b>bold text</b>", "<b>bold text</b>"},
		{"strong", "<strong>strong text</strong>", "<strong>strong text</strong>"},
		{"italic", "<i>italic text</i>", "<i>italic text</i>"},
		{"emphasis", "<em>emphasized text</em>", "<em>emphasized text</em>"},
		{"underline", "<u>underlined text</u>", "<u>underlined text</u>"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeHTML(tt.input)
			if !strings.Contains(result, tt.contains) {
				t.Errorf("Expected %q to contain %q", result, tt.contains)
			}
		})
	}
}

// TestSanitizeHTMLDangerousProtocols tests that dangerous URL protocols are blocked.
func TestSanitizeHTMLDangerousProtocols(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"javascript", `<a href="javascript:alert('xss')">Click</a>`},
		{"data", `<img src="data:text/html,<script>alert('xss')</script>" />`},
		{"vbscript", `<a href="vbscript:msgbox">Click</a>`},
		{"file", `<a href="file:///etc/passwd">Click</a>`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeHTML(tt.input)
			// Dangerous URLs should not have href/src attributes
			if strings.Contains(result, `href="javascript:`) {
				t.Error("javascript: protocol should be blocked")
			}
			if strings.Contains(result, `src="data:`) {
				t.Error("data: protocol should be blocked")
			}
			if strings.Contains(result, `href="vbscript:`) {
				t.Error("vbscript: protocol should be blocked")
			}
			if strings.Contains(result, `href="file:`) {
				t.Error("file: protocol should be blocked")
			}
		})
	}
}

// TestSanitizeHTMLXSSAttempts tests various XSS attack vectors.
func TestSanitizeHTMLXSSAttempts(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"script tag", `<script>alert('xss')</script>`},
		{"iframe", `<iframe src="evil.com"></iframe>`},
		{"object", `<object data="evil.swf"></object>`},
		{"embed", `<embed src="evil.swf">`},
		{"form", `<form action="evil.com"><input type="submit"></form>`},
		{"event handler", `<img src="x" onerror="alert('xss')">`},
		{"svg", `<svg onload="alert('xss')"></svg>`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeHTML(tt.input)
			// Should not contain the dangerous tag
			if strings.Contains(result, "<script") {
				t.Error("Script tag should be escaped")
			}
			if strings.Contains(result, "<iframe") {
				t.Error("Iframe tag should be escaped")
			}
			if strings.Contains(result, "<object") {
				t.Error("Object tag should be escaped")
			}
			if strings.Contains(result, "<embed") {
				t.Error("Embed tag should be escaped")
			}
			if strings.Contains(result, "<form") {
				t.Error("Form tag should be escaped")
			}
			if strings.Contains(result, "onerror=") {
				t.Error("Event handlers should be stripped")
			}
			if strings.Contains(result, "<svg") {
				t.Error("SVG tag should be escaped")
			}
		})
	}
}

// TestSanitizeHTMLPlaceholders tests that dangerous media tags show placeholders.
func TestSanitizeHTMLPlaceholders(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		shouldContain string
	}{
		{"iframe with URL", `<iframe src="https://youtube.com/embed/xyz"></iframe>`, "[iframe: <a href=\"https://youtube.com/embed/xyz\">"},
		{"iframe without src", `<iframe></iframe>`, "[replaced iframe]"},
		{"video", `<video src="video.mp4"></video>`, "[replaced video]"},
		{"embed", `<embed src="flash.swf">`, "[replaced embed]"},
		{"object", `<object data="plugin.swf"></object>`, "[replaced object]"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeHTML(tt.input)
			if !strings.Contains(result, tt.shouldContain) {
				t.Errorf("Expected %q to be present in output, got: %q", tt.shouldContain, result)
			}
			// Ensure the actual dangerous tag is not present
			if strings.Contains(result, "<iframe ") || strings.Contains(result, "<iframe>") {
				t.Error("Dangerous iframe tag should not be present in output")
			}
			if strings.Contains(result, "<video") {
				t.Error("Dangerous video tag should not be present in output")
			}
			if strings.Contains(result, "<embed") {
				t.Error("Dangerous embed tag should not be present in output")
			}
			if strings.Contains(result, "<object") {
				t.Error("Dangerous object tag should not be present in output")
			}
		})
	}
}

// TestSanitizeHTMLBicycleThreadPost tests sanitization of post #53741499 from the Bicycle thread.
// This post contains an iframe with a video/image that should be replaced with a clickable link.
func TestSanitizeHTMLBicycleThreadPost(t *testing.T) {
	// Real HTML from ADVRider post #53741499
	input := `<iframe width="640" height="360" src="https://www.youtube.com/embed/xyz123" frameborder="0" allowfullscreen=""></iframe>`

	result := sanitizeHTML(input)

	// Test 1: Iframe should be replaced with link placeholder
	if !strings.Contains(result, "[iframe:") {
		t.Error("Iframe should be replaced with [iframe: ...] placeholder containing link")
	}

	// Test 2: Should contain clickable link to YouTube
	if !strings.Contains(result, `<a href="https://www.youtube.com/embed/xyz123">`) {
		t.Error("Should contain clickable link to the iframe source URL")
	}

	// Test 3: Link text should show the URL
	if !strings.Contains(result, "https://www.youtube.com/embed/xyz123</a>") {
		t.Error("Link text should show the URL")
	}

	// Test 4: No actual iframe tag should be present
	if strings.Contains(result, "<iframe") {
		t.Error("Iframe tag should not be present in output")
	}

	// Test 5: Dangerous attributes should not be present
	if strings.Contains(result, "frameborder") {
		t.Error("frameborder attribute should not be present")
	}
	if strings.Contains(result, "allowfullscreen") {
		t.Error("allowfullscreen attribute should not be present")
	}
}

// TestEscapeHTML tests the basic HTML escaping function.
func TestEscapeHTML(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"<script>", "&lt;script&gt;"},
		{"hello & goodbye", "hello &amp; goodbye"},
		{`"quotes"`, "&quot;quotes&quot;"},
		{"it's", "it&#39;s"},
		{"<b>test</b>", "&lt;b&gt;test&lt;/b&gt;"},
	}

	for _, tt := range tests {
		result := escapeHTML(tt.input)
		if result != tt.expected {
			t.Errorf("escapeHTML(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

// TestIsSafeURL tests the URL safety validator.
func TestIsSafeURL(t *testing.T) {
	tests := []struct {
		url  string
		safe bool
	}{
		{"https://advrider.com/f/threads/test.123/", true},
		{"http://example.com", true},
		{"/relative/path", true},
		{"./relative", true},
		{"../relative", true},
		{"image.jpg", true},
		{"javascript:alert('xss')", false},
		{"data:text/html,<script>alert('xss')</script>", false},
		{"vbscript:msgbox", false},
		{"file:///etc/passwd", false},
		{"about:blank", false},
	}

	for _, tt := range tests {
		result := isSafeURL(tt.url)
		if result != tt.safe {
			t.Errorf("isSafeURL(%q) = %v, want %v", tt.url, result, tt.safe)
		}
	}
}

// TestExtractAttribute tests the attribute extraction helper.
func TestExtractAttribute(t *testing.T) {
	tests := []struct {
		tag      string
		attr     string
		expected string
	}{
		{`img src="test.jpg" alt="test"`, "src", "test.jpg"},
		{`img src="test.jpg" alt="test"`, "alt", "test"},
		{`a href='https://example.com' class="link"`, "href", "https://example.com"},
		{`img src="https://advrider.com/f/attachments/photo.jpg" alt="Photo"`, "src", "https://advrider.com/f/attachments/photo.jpg"},
		{`a href="test.html"`, "href", "test.html"},
		{`img alt="no src"`, "src", ""}, // No src attribute
	}

	for _, tt := range tests {
		result := extractAttribute(tt.tag, tt.attr)
		if result != tt.expected {
			t.Errorf("extractAttribute(%q, %q) = %q, want %q", tt.tag, tt.attr, result, tt.expected)
		}
	}
}

// TestSanitizeHTMLBicyclePost53741781 tests sanitization of real post #53741781 from the Bicycle thread.
// This post contains nested blockquotes, br tags, and links that must be preserved correctly.
// URL: https://advrider.com/f/threads/bicycle-thread.150964/page-5412#post-53741781
func TestSanitizeHTMLBicyclePost53741781(t *testing.T) {
	// Real HTML from ADVRider post #53741781 by MN_Smurf
	input := `<div class="bbCodeBlock bbCodeQuote" data-author="Mambo Danny">
	<aside>

			<div class="attribution type">Mambo Danny said:

					<a href="goto/post?id=53722273#post-53722273" class="AttributionLink">↑</a>

			</div>

		<blockquote class="quoteContainer"><div class="quote">I tried pliers and vise-grips on both, to a moderate amount of force, neither moved.  I can try two sets of vise-grips next.  It was Yoeleo who built the wheels; if it were me I would have used synthetic boat-trailer-bearing grease as the nipple lube.  Of course I don&#39;t have any idea if they used any lube at all, nor if my syn grease would have lasted all that time with salt water exposure.<br/>
<br/>
I&#39;m thanking my lucky stars as I had it in the back of my head that I could buy someone&#39;s used tri-bike/time-trial-bike cheap from the mid 20-teens, many of which had aluminum wheels, switch these over to it and have something else to ride.  There was a chance I would have got out there and started moving at 25 MPH or over when one of the wheels would fail.  But the hard use / hard environment is part of why I relegated that carbon fiber Planet X bike to the indoor trainer only.  I&#39;ve been asked why I don&#39;t ride it, and it&#39;s because I don&#39;t trust it anymore.<br/>
<br/>
In time I&#39;ll try penetrating oil on each nipple, let that soak a couple days.  Maybe ... maybe... some very focused heat source on the nipple only while trying to keep it away from the carbon fiber rim?  Not sure if that would be like a jet-lighter, maybe the soldering iron?</div><div class="quoteExpand">Click to expand...</div></blockquote>
	</aside>
</div>Hate to say it, but just replace the spokes.  Unlike steel, aluminum adds material when it corrodes.  Steel spokes into aluminum nipples in a salt air environment has effectively welded that joint together with galvanic corrosion.  You&#39;re going to destroy the parts trying to get them apart.`

	result := sanitizeHTML(input)

	// Test 1: BR tags should be preserved (not escaped)
	if !strings.Contains(result, "<br>") {
		t.Error("BR tags should be preserved as actual tags, not escaped")
	}
	if strings.Contains(result, "&lt;br") {
		t.Error("BR tags should NOT be HTML-escaped")
	}

	// Test 2: Blockquotes should be preserved
	if !strings.Contains(result, "<blockquote>") {
		t.Error("Blockquote tags should be preserved")
	}

	// Test 3: Links should be preserved with href
	if !strings.Contains(result, `<a href="goto/post?id=53722273#post-53722273">`) {
		t.Error("Link href attributes should be preserved")
	}
	if !strings.Contains(result, "↑</a>") {
		t.Error("Link content should be preserved")
	}

	// Test 4: Div tags should be preserved
	if !strings.Contains(result, "<div>") {
		t.Error("Div tags should be preserved")
	}

	// Test 5: Text content should be fully preserved
	if !strings.Contains(result, "I tried pliers and vise-grips") {
		t.Error("Quote text content should be preserved")
	}
	if !strings.Contains(result, "Hate to say it, but just replace the spokes") {
		t.Error("Main post text should be preserved")
	}
	if !strings.Contains(result, "galvanic corrosion") {
		t.Error("Technical terms should be preserved")
	}

	// Test 6: HTML entities should be preserved (not double-escaped)
	if !strings.Contains(result, "don&#39;t") {
		t.Error("HTML entities like &#39; should be preserved as-is")
	}
	if strings.Contains(result, "&amp;#39;") {
		t.Error("HTML entities should NOT be double-escaped")
	}

	// Test 7: Dangerous attributes should be stripped
	if strings.Contains(result, `data-author=`) {
		t.Error("data-author attribute should be stripped from divs")
	}
	if strings.Contains(result, `class="bbCodeQuote"`) {
		t.Error("class attributes should be stripped")
	}
	if strings.Contains(result, `class="AttributionLink"`) {
		t.Error("class attributes should be stripped from links")
	}

	// Test 8: Aside tags are not in whitelist and should be escaped
	if strings.Contains(result, "<aside>") {
		t.Error("Aside tags should be escaped (not in whitelist)")
	}
}
