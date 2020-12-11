module atlas

go 1.15

require (
	github.com/BurntSushi/toml v0.3.1
	github.com/antonfisher/nested-logrus-formatter v1.3.0
	github.com/atlasdatatech/go-gpkg v0.0.0-20200624104138-252bcd3cfe22
	github.com/axgle/mahonia v0.0.0-20180208002826-3358181d7394
	github.com/casbin/casbin v1.9.1
	github.com/casbin/gorm-adapter v1.0.0
	github.com/cockroachdb/apd v1.1.0 // indirect
	github.com/dgrijalva/jwt-go v3.2.0+incompatible
	github.com/didip/tollbooth v4.0.2+incompatible
	github.com/fogleman/gg v1.3.0
	github.com/gin-contrib/cors v1.3.1
	github.com/gin-gonic/contrib v0.0.0-20201101042839-6a891bf89f19
	github.com/gin-gonic/gin v1.6.3
	github.com/go-spatial/geom v0.0.0-20190821234737-802ab2533ab4
	github.com/go-spatial/tegola v0.12.1
	github.com/gofrs/uuid v3.3.0+incompatible // indirect
	github.com/golang/freetype v0.0.0-20170609003504-e2365dfdc4a0 // indirect
	github.com/golang/protobuf v1.4.2
	github.com/jackc/fake v0.0.0-20150926172116-812a484cc733 // indirect
	github.com/jinzhu/gorm v1.9.16
	github.com/jonas-p/go-shp v0.1.1
	github.com/lib/pq v1.8.0
	github.com/mattn/go-sqlite3 v1.14.0
	github.com/nfnt/resize v0.0.0-20180221191011-83c6a9932646
	github.com/onsi/ginkgo v1.14.2 // indirect
	github.com/onsi/gomega v1.10.3 // indirect
	github.com/patrickmn/go-cache v2.1.0+incompatible // indirect
	github.com/paulmach/orb v0.1.6
	github.com/shiena/ansicolor v0.0.0-20200904210342-c7312218db18
	github.com/shopspring/decimal v1.2.0 // indirect
	github.com/sirupsen/logrus v1.7.0
	github.com/spf13/viper v1.7.1
	github.com/stretchr/testify v1.4.0
	github.com/teris-io/shortid v0.0.0-20171029131806-771a37caa5cf
	golang.org/x/crypto v0.0.0-20200622213623-75b288015ac9
	golang.org/x/text v0.3.3
	gopkg.in/alexcesaro/quotedprintable.v3 v3.0.0-20150716171945-2caba252f4dc // indirect
	gopkg.in/gomail.v2 v2.0.0-20160411212932-81ebce5c23df
)

replace github.com/go-spatial/tegola v0.12.1 => github.com/atlasdatatech/tegola v0.13.4

replace github.com/paulmach/orb v0.1.6 => github.com/atlasdatatech/orb v0.2.2
