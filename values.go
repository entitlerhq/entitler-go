package entitler

import (
	"slices"
	"strconv"
)

// Value is a feature's value: [On], an [Amount] (0 included), or [Unlimited]. Whether the customer
// is entitled is the server's decision, Check.Entitled, never derived from the value: an on/off
// feature the customer is not entitled to answers Amount(0).
type Value interface {
	// String answers "on", the amount in decimal, or "unlimited".
	String() string
	isValue()
}

// On is the value of an on/off feature the customer has (JSON true).
type On struct{}

// Amount is a whole-number value: a config value, a metered allowance or what remains of it.
type Amount int64

// Unlimited is a value with no limit (JSON "unlimited").
type Unlimited struct{}

func (On) String() string { return "on" }

func (a Amount) String() string { return strconv.FormatInt(int64(a), 10) }

func (Unlimited) String() string { return "unlimited" }

func (On) isValue()        {}
func (Amount) isValue()    {}
func (Unlimited) isValue() {}

// FeatureType is a feature's type. It is an open set: a type this version does not know is kept as it is.
type FeatureType string

// The feature types.
const (
	// FeatureTypeBoolean is an on/off feature.
	FeatureTypeBoolean FeatureType = "boolean"
	// FeatureTypeConfig is a feature with a configured whole-number value, such as seats.
	FeatureTypeConfig FeatureType = "config"
	// FeatureTypeMetered is a feature whose usage is counted against an allowance.
	FeatureTypeMetered FeatureType = "metered"
	// FeatureTypeGroup is a group of other features.
	FeatureTypeGroup FeatureType = "group"
)

// Feature names a feature in a method's arguments: a generated constant ([OnOffFeature],
// [ConfigFeature], [MeteredFeature] or [GroupFeature]) or a plain [Key].
type Feature interface {
	// Key answers the feature's key, such as "export_pdf".
	Key() string
	// Type answers the feature's type, or "" for a plain Key.
	Type() FeatureType
}

// Key is a feature named by its key alone, for code that does not use generated constants:
// entitler.Key("export_pdf"). Usage methods take a [MeteredFeature] instead: [Metered].
type Key string

// Key answers the key.
func (k Key) Key() string { return string(k) }

// Type answers "", since a plain key does not say its type.
func (Key) Type() FeatureType { return "" }

// OnOffFeature is an on/off feature constant.
type OnOffFeature struct{ key string }

// ConfigFeature is a config feature constant, whose value is a whole number such as seats.
type ConfigFeature struct{ key string }

// MeteredFeature is a metered feature constant, the only kind usage methods take.
type MeteredFeature struct{ key string }

// GroupFeature is a group feature constant, with its leaf members' keys for documentation.
type GroupFeature struct {
	key      string
	includes []string
}

// OnOff declares an on/off feature by hand.
func OnOff(key string) OnOffFeature { return OnOffFeature{key: key} }

// Config declares a config feature by hand.
func Config(key string) ConfigFeature { return ConfigFeature{key: key} }

// Metered declares a metered feature by hand, for the usage methods: entitler.Metered("ai_credits").
func Metered(key string) MeteredFeature { return MeteredFeature{key: key} }

// Group declares a group feature by hand, with the keys of its leaf members.
func Group(key string, includes ...string) GroupFeature {
	return GroupFeature{key: key, includes: slices.Clone(includes)}
}

// Key answers the feature's key.
func (f OnOffFeature) Key() string { return f.key }

// Type answers FeatureTypeBoolean.
func (OnOffFeature) Type() FeatureType { return FeatureTypeBoolean }

// Key answers the feature's key.
func (f ConfigFeature) Key() string { return f.key }

// Type answers FeatureTypeConfig.
func (ConfigFeature) Type() FeatureType { return FeatureTypeConfig }

// Key answers the feature's key.
func (f MeteredFeature) Key() string { return f.key }

// Type answers FeatureTypeMetered.
func (MeteredFeature) Type() FeatureType { return FeatureTypeMetered }

// Key answers the feature's key.
func (f GroupFeature) Key() string { return f.key }

// Type answers FeatureTypeGroup.
func (GroupFeature) Type() FeatureType { return FeatureTypeGroup }

// Includes answers the keys of the group's leaf members.
func (f GroupFeature) Includes() []string { return slices.Clone(f.includes) }

func featureKey(f Feature) (string, error) {
	if f == nil || blank(f.Key()) {
		return "", argError("name the feature by its key")
	}
	return f.Key(), nil
}
