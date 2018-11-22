package main

// Constants representing FieldType types
const (
	TypeUnkown      = "unkown"  // encoding = gzip
	TypeString      = "string"  // encoding = gzip
	TypeInteger     = "integer" // encoding = deflate
	TypeReal        = "real"
	TypeDate        = "date"
	TypeBool        = "bool"
	TypeStringArray = "string_array"
	TypeGeojson     = "geojson"
)

// A list of the datasets types that are currently supported.
const (
	TypePoint      = "Point"
	TypeLineString = "LineString"
	TypePolygon    = "Polygon"
	TypeAttribute  = "Attribute" //属性数据表,non-spatial
)
