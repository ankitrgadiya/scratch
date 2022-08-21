package markdown

import (
	wikilink "github.com/abhinav/goldmark-wikilink"
	"github.com/yuin/goldmark"
)

var (
	_hash = []byte{'#'}
)

func WikiLinkExtension() goldmark.Extender {
	return &wikilink.Extender{
		Resolver: new(wikilinkResolver),
	}
}

type wikilinkResolver struct{}

func (wikilinkResolver) ResolveWikilink(n *wikilink.Node) ([]byte, error) {
	dest := make([]byte, len(n.Target)+len(_hash)+len(n.Fragment))
	var i int
	if len(n.Target) > 0 {
		i += copy(dest, n.Target)
	}
	if len(n.Fragment) > 0 {
		i += copy(dest[i:], _hash)
		i += copy(dest[i:], n.Fragment)
	}
	return dest[:i], nil
}
