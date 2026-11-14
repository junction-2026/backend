from app.textutil import html_to_text, to_rfc3339


def test_mastodon_html_stripped():
    assert html_to_text('<p>Hello &amp; <a href="https://x.y">welcome</a></p><p>Bye</p>') == "Hello & welcome\nBye"


def test_br_and_entities_and_trim():
    assert html_to_text("  <p>a<br>b<br/>c &lt;3 &#39;q&#39;</p>\n") == "a\nb\nc <3 'q'"


def test_empty():
    assert html_to_text(None) == "" and html_to_text("") == "" and html_to_text("<p></p>") == ""


def test_timestamps():
    assert to_rfc3339("2026-11-14T09:30:15.123Z") == "2026-11-14T09:30:15Z"
    assert to_rfc3339("2026-11-14T11:30:15+02:00") == "2026-11-14T09:30:15Z"
    assert to_rfc3339("garbage") is None and to_rfc3339(None) is None
