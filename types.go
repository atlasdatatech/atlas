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
var CRSs = []CRS{WGS84, CGCS2000, GCJ02, BD09}

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
var Encodings = []Encoding{UTF8, GBK, BIG5, GB18030}

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
var FieldTypes = []FieldType{String, Int, Float, Date, Bool}

// DataFormat is an enum that defines the data format of a file
type DataFormat string

// Constants representing TileFormat types
const (
	// ZIPEXT     DataFormat = ".zip"
	ZIPEXT     = ".zip"
	CSVEXT     = ".csv"
	SHPEXT     = ".shp"
	KMLEXT     = ".kml"
	GPXEXT     = ".gpx"
	GEOJSONEXT = ".geojson"
)

//DataFormats 数据类型集合
var DataFormats = []DataFormat{ZIPEXT, CSVEXT, SHPEXT, KMLEXT, GPXEXT, GEOJSONEXT}

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

//TileFormats 支持的瓦片类型
var TileFormats = []TileFormat{GZIP, ZLIB, PNG, JPG, PBF, WEBP}

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

//TaskType 任务类型
type TaskType string

// Constants representing TileFormat types
const (
	DSIMPORT TaskType = "dsimport" // encoding = gzip
	TSIMPORT          = "tsimport" // encoding = deflate
	DS2TS             = "ds2ts"    // encoding = deflate
)

//TaskTypes 支持的瓦片类型
var TaskTypes = []TaskType{DSIMPORT, TSIMPORT}

//GeoType 几何类型
type GeoType string

// A list of the datasets types that are currently supported.
const (
	Point           GeoType = "Point"
	MultiPoint              = "MultiPoint"
	LineString              = "LineString"
	MultiLineString         = "MultiLineString"
	Polygon                 = "Polygon"
	MultiPolygon            = "MultiPolygon"
	Attribute               = "Attribute" //属性数据表,non-spatial
)

//GeoTypes 支持的字段类型
var GeoTypes = []GeoType{Point, MultiPoint, LineString, MultiLineString, Polygon, MultiPolygon, Attribute}
