package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestBaiduRespConvert(t *testing.T) {

	body := strings.NewReader(
		`{
		"status":0,
		"message":"ok",
		"results":[
			{
				"name":"方正智谷-北门",
				"location":{
					"lat":31.351,
					"lng":120.784912
				},
				"address":"苏虹东路177号",
				"province":"江苏省",
				"city":"苏州市",
				"area":"苏州工业园区"
			}
		]
	}`)

	outstr := `{
		"status": 0,
		"message": "ok",
		"results": [
			{
				"name": "方正智谷-北门",
				"location": {
					"lat": 31.347354180740417,
					"lng": 120.77394720739622
				},
				"address": "苏虹东路177号",
				"province": "江苏省",
				"city": "苏州市",
				"district": "苏州工业园区"
			}
		]
	}`

	out := RespOut{}
	json.Unmarshal([]byte(outstr), &out)

	res := baiduRespConvert(body)

	if !reflect.DeepEqual(res, out) {
		t.Errorf("baiduRespConvert() output:\ngot  %v\nwant %v", res, out)
	}
}
