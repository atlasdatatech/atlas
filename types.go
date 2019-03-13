package main

// CRS coordinate reference system
type CRS string

// Supported CRSs
const (
	WGS84    CRS = "WGS84"
	CGCS2000     = "CGCS2000"
	GCJ02        = "GCJ02"
	BD09         = "BD09"
)

//CRSs 支持的坐标系
var CRSs = []string{"WGS84", "CGCS2000", "GCJ02", "BD09"}

//Encoding text encoding
type Encoding string

// Supported encodings
const (
	UTF8    Encoding = "utf-8"
	GBK              = "gbk"
	BIG5             = "big5"
	GB18030          = "gb18030"
)

//Encodings 支持的编码格式
var Encodings = []string{"utf-8", "gbk", "big5", "gb18030"}

// FieldType is a convenience alias that can be used for a more type safe way of
// reason and use Series types.
type FieldType string

// Supported Series Types
const (
	String      FieldType = "string"
	Bool                  = "bool"
	Int                   = "int"
	Float                 = "float"
	Date                  = "date"
	StringArray           = "string_array"
	Geojson               = "geojson"
)

//FieldTypes 支持的字段类型
var FieldTypes = []string{"string", "int", "float", "bool", "date"}

// TileFormat is an enum that defines the tile format of a tile
type TileFormat string

// Constants representing TileFormat types
const (
	GZIP TileFormat = "gzip" // encoding = gzip
	ZLIB            = "zlib" // encoding = deflate
	PNG             = "png"
	JPG             = "jpg"
	PBF             = "pbf"
	WEBP            = "webp"
)

// ContentType returns the MIME content type of the tile
func (t TileFormat) ContentType() string {
	switch t {
	case PNG:
		return "image/png"
	case JPG:
		return "image/jpeg"
	case PBF:
		return "application/x-protobuf" // Content-Encoding header must be gzip
	case WEBP:
		return "image/webp"
	default:
		return ""
	}
}

// A list of the datasets types that are currently supported.
const (
	Point           = "Point"
	MultiPoint      = "MultiPoint"
	LineString      = "LineString"
	MultiLineString = "MultiLineString"
	Polygon         = "Polygon"
	MultiPolygon    = "MultiPolygon"
	Attribute       = "Attribute" //属性数据表,non-spatial
)

//GeomTypes 支持的字段类型
var GeomTypes = []string{"Point", "MultiPoint", "LineString", "MultiLineString", "Polygon", "MultiPolygon", "Attribute"}
