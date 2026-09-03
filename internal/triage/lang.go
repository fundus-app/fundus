package triage

import "strings"

// guessLanguage names the language of text when common function words make
// it obvious, and returns "" otherwise. It exists only to phrase the language
// rule concretely for small models, which otherwise drift into the language
// of the surrounding context ("Europe/Berlin", German notes) for an English
// capture. A wrong guess would be worse than none, so it needs a clear lead.
func guessLanguage(text string) string {
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r == 'ä' || r == 'ö' || r == 'ü' || r == 'ß' || r == 'é' || r == 'è' || r == 'à' || r == 'ç' || r == 'ñ' || r == 'í' || r == 'ó' || r == 'á' || r == '\'')
	})
	if len(words) < 3 {
		return ""
	}
	best, second, name := 0, 0, ""
	for lang, stop := range stopwords {
		hits := 0
		seen := map[string]bool{}
		for _, w := range words {
			if stop[w] && !seen[w] {
				seen[w] = true
				hits++
			}
		}
		switch {
		case hits > best:
			second, best, name = best, hits, lang
		case hits > second:
			second = hits
		}
	}
	if best >= 2 && best > second {
		return name
	}
	return ""
}

func set(words string) map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(words) {
		m[w] = true
	}
	return m
}

var stopwords = map[string]map[string]bool{
	"English": set("the a an and or but of to in on at for with by from is are was were be been this that these those it its i you we they he she my your our their not no if then than there here what which who when how must should could would can will do does did have has had otherwise also about into"),
	"German":  set("der die das ein eine einen einem einer und oder aber von zu im in auf für mit bei aus ist sind war waren sein ich du wir ihr sie er es mein dein unser nicht kein keine wenn dann als dort hier was welche wer wann wie muss soll sollte könnte würde kann wird noch auch schon mal nach über bis dass ob"),
	"French":  set("le la les un une des et ou mais de du au aux en dans sur pour avec par est sont était je tu nous vous ils elles il elle mon ma mes ton votre notre ne pas si alors que qui quand comment doit dois devrait pourrait peut sera"),
	"Spanish": set("el la los las un una unos unas y o pero de del al en sobre para con por es son era yo tú nosotros ellos ellas él ella mi tu su nuestro no si entonces que quien cuando cómo debe debería podría puede será también"),
	"Italian": set("il lo la i gli le un uno una e o ma di del della al alla in su per con da è sono era io tu noi voi loro lui lei mio tuo suo nostro non se allora che chi quando come deve dovrebbe potrebbe può sarà anche"),
	"Dutch":   set("de het een en of maar van naar in op voor met bij uit is zijn was waren ik jij wij jullie zij hij ze mijn jouw ons hun niet geen als dan daar hier wat welke wie wanneer hoe moet zou kan zal ook nog"),
}
