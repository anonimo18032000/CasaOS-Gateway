package route

import (
	"crypto/ecdsa"
	"io"
	"net/http"
	"strconv"

	"github.com/IceWhaleTech/CasaOS-Common/external"
	"github.com/IceWhaleTech/CasaOS-Common/model"
	"github.com/IceWhaleTech/CasaOS-Common/utils/common_err"
	"github.com/IceWhaleTech/CasaOS-Common/utils/jwt"
	"github.com/IceWhaleTech/CasaOS-Gateway/service"
	"github.com/labstack/echo/v4"
	echo_middleware "github.com/labstack/echo/v4/middleware"
)

// maxCertificateUploadSize caps individual cert/key uploads - real certificates and keys are a few KB at most.
const maxCertificateUploadSize = 1 << 20 // 1 MiB

type HTTPSStatusResponse struct {
	Enabled bool   `json:"enabled"`
	Domain  string `json:"domain"`
	Port    string `json:"port"`
}

type GenerateHTTPSRequest struct {
	Domain string `json:"domain"`
}

type ManagementRoute struct {
	management *service.Management
}

func NewManagementRoute(management *service.Management) *ManagementRoute {
	return &ManagementRoute{
		management: management,
	}
}

func (m *ManagementRoute) GetRoute() http.Handler {
	e := echo.New()

	e.Use((echo_middleware.CORSWithConfig(echo_middleware.CORSConfig{
		AllowOrigins:     []string{"*"},
		AllowMethods:     []string{echo.POST, echo.GET, echo.OPTIONS, echo.PUT, echo.DELETE},
		AllowHeaders:     []string{echo.HeaderAuthorization, echo.HeaderContentLength, echo.HeaderXCSRFToken, echo.HeaderContentType, echo.HeaderAccessControlAllowOrigin, echo.HeaderAccessControlAllowHeaders, echo.HeaderAccessControlAllowMethods, echo.HeaderConnection, echo.HeaderOrigin, echo.HeaderXRequestedWith},
		ExposeHeaders:    []string{echo.HeaderContentLength, echo.HeaderAccessControlAllowOrigin, echo.HeaderAccessControlAllowHeaders},
		MaxAge:           172800,
		AllowCredentials: true,
	})))

	e.Use(echo_middleware.Gzip())

	e.GET("/ping", func(ctx echo.Context) error {
		return ctx.JSON(http.StatusOK, echo.Map{
			"message": "pong from management service",
		})
	})

	m.buildV1Group(e)

	return e
}

func (m *ManagementRoute) buildV1Group(e *echo.Echo) {
	v1Group := e.Group("/v1")

	v1Group.Use()
	{
		m.buildV1RouteGroup(v1Group)
	}
}

func (m *ManagementRoute) buildV1RouteGroup(v1Group *echo.Group) {
	v1GatewayGroup := v1Group.Group("/gateway")

	v1GatewayGroup.Use()
	{
		v1GatewayGroup.GET("/routes", func(ctx echo.Context) error {
			return ctx.JSON(http.StatusOK, m.management.GetRoutes())
		})

		v1GatewayGroup.POST("/routes",
			func(ctx echo.Context) error {
				var route *model.Route
				err := ctx.Bind(&route)
				if err != nil {
					return ctx.JSON(http.StatusBadRequest, model.Result{
						Success: common_err.CLIENT_ERROR,
						Message: err.Error(),
					})
				}

				if err := m.management.CreateRoute(route); err != nil {
					return ctx.JSON(http.StatusInternalServerError, model.Result{
						Success: common_err.SERVICE_ERROR,
						Message: err.Error(),
					})
				}

				return ctx.NoContent(http.StatusCreated)
			},
			echo_middleware.JWTWithConfig(echo_middleware.JWTConfig{
				Skipper: func(c echo.Context) bool {
					return c.RealIP() == "::1" || c.RealIP() == "127.0.0.1"
					// return true
				},
				ParseTokenFunc: func(token string, c echo.Context) (interface{}, error) {
					valid, claims, err := jwt.Validate(token, func() (*ecdsa.PublicKey, error) { return external.GetPublicKey(m.management.State.GetRuntimePath()) })
					if err != nil || !valid {
						return nil, echo.ErrUnauthorized
					}
					c.Request().Header.Set("user_id", strconv.Itoa(claims.ID))

					return claims, nil
				},
				TokenLookupFuncs: []echo_middleware.ValuesExtractor{
					func(c echo.Context) ([]string, error) {
						if len(c.Request().Header.Get(echo.HeaderAuthorization)) > 0 {
							return []string{c.Request().Header.Get(echo.HeaderAuthorization)}, nil
						}
						return []string{c.QueryParam("token")}, nil
					},
				},
			}))

		v1GatewayGroup.GET("/port", func(ctx echo.Context) error {
			return ctx.JSON(http.StatusOK, model.Result{
				Success: common_err.SUCCESS,
				Message: common_err.GetMsg(common_err.SUCCESS),
				Data:    m.management.GetGatewayPort(),
			})
		})

		v1GatewayGroup.PUT("/port",
			func(ctx echo.Context) error {
				var request *model.ChangePortRequest

				if err := ctx.Bind(&request); err != nil {
					return ctx.JSON(http.StatusBadRequest, model.Result{
						Success: common_err.CLIENT_ERROR,
						Message: err.Error(),
					})
				}

				if err := m.management.SetGatewayPort(request.Port); err != nil {
					return ctx.JSON(http.StatusInternalServerError, model.Result{
						Success: common_err.SERVICE_ERROR,
						Message: err.Error(),
					})
				}

				return ctx.JSON(http.StatusOK, model.Result{
					Success: common_err.SUCCESS,
					Message: common_err.GetMsg(common_err.SUCCESS),
				})
			},
			echo_middleware.JWTWithConfig(echo_middleware.JWTConfig{
				Skipper: func(c echo.Context) bool {
					return c.RealIP() == "::1" || c.RealIP() == "127.0.0.1"
					// return true
				},
				ParseTokenFunc: func(token string, c echo.Context) (interface{}, error) {
					valid, claims, err := jwt.Validate(token, func() (*ecdsa.PublicKey, error) { return external.GetPublicKey(m.management.State.GetRuntimePath()) })
					if err != nil || !valid {
						return nil, echo.ErrUnauthorized
					}
					c.Request().Header.Set("user_id", strconv.Itoa(claims.ID))

					return claims, nil
				},
				TokenLookupFuncs: []echo_middleware.ValuesExtractor{
					func(c echo.Context) ([]string, error) {
						if len(c.Request().Header.Get(echo.HeaderAuthorization)) > 0 {
							return []string{c.Request().Header.Get(echo.HeaderAuthorization)}, nil
						}
						return []string{c.QueryParam("token")}, nil
					},
				},
			}))

		v1GatewayGroup.GET("/https", func(ctx echo.Context) error {
			tlsState := m.management.GetGatewayTLS()

			return ctx.JSON(http.StatusOK, model.Result{
				Success: common_err.SUCCESS,
				Message: common_err.GetMsg(common_err.SUCCESS),
				Data: HTTPSStatusResponse{
					Enabled: tlsState.Enabled,
					Domain:  tlsState.Domain,
					Port:    tlsState.Port,
				},
			})
		})

		v1GatewayGroup.POST("/https/generate",
			func(ctx echo.Context) error {
				var request *GenerateHTTPSRequest
				if err := ctx.Bind(&request); err != nil || request == nil || request.Domain == "" {
					return ctx.JSON(http.StatusBadRequest, model.Result{
						Success: common_err.CLIENT_ERROR,
						Message: "domain is required",
					})
				}

				if err := m.management.GenerateHTTPSCertificate(request.Domain); err != nil {
					return ctx.JSON(http.StatusInternalServerError, model.Result{
						Success: common_err.SERVICE_ERROR,
						Message: err.Error(),
					})
				}

				return ctx.JSON(http.StatusOK, model.Result{
					Success: common_err.SUCCESS,
					Message: common_err.GetMsg(common_err.SUCCESS),
				})
			},
			m.jwtMiddleware(),
		)

		v1GatewayGroup.POST("/https/upload",
			func(ctx echo.Context) error {
				certBytes, err := readFormFile(ctx, "cert")
				if err != nil {
					return ctx.JSON(http.StatusBadRequest, model.Result{
						Success: common_err.CLIENT_ERROR,
						Message: err.Error(),
					})
				}

				keyBytes, err := readFormFile(ctx, "key")
				if err != nil {
					return ctx.JSON(http.StatusBadRequest, model.Result{
						Success: common_err.CLIENT_ERROR,
						Message: err.Error(),
					})
				}

				if err := m.management.UploadHTTPSCertificate(certBytes, keyBytes); err != nil {
					return ctx.JSON(http.StatusBadRequest, model.Result{
						Success: common_err.CLIENT_ERROR,
						Message: err.Error(),
					})
				}

				return ctx.JSON(http.StatusOK, model.Result{
					Success: common_err.SUCCESS,
					Message: common_err.GetMsg(common_err.SUCCESS),
				})
			},
			m.jwtMiddleware(),
		)

		v1GatewayGroup.POST("/https/disable",
			func(ctx echo.Context) error {
				if err := m.management.DisableHTTPS(); err != nil {
					return ctx.JSON(http.StatusInternalServerError, model.Result{
						Success: common_err.SERVICE_ERROR,
						Message: err.Error(),
					})
				}

				return ctx.JSON(http.StatusOK, model.Result{
					Success: common_err.SUCCESS,
					Message: common_err.GetMsg(common_err.SUCCESS),
				})
			},
			m.jwtMiddleware(),
		)
	}
}

// jwtMiddleware mirrors the JWT config used by the existing /routes and /port endpoints above,
// factored out so new mutating endpoints don't have to repeat the whole block.
func (m *ManagementRoute) jwtMiddleware() echo.MiddlewareFunc {
	return echo_middleware.JWTWithConfig(echo_middleware.JWTConfig{
		Skipper: func(c echo.Context) bool {
			return c.RealIP() == "::1" || c.RealIP() == "127.0.0.1"
		},
		ParseTokenFunc: func(token string, c echo.Context) (interface{}, error) {
			valid, claims, err := jwt.Validate(token, func() (*ecdsa.PublicKey, error) { return external.GetPublicKey(m.management.State.GetRuntimePath()) })
			if err != nil || !valid {
				return nil, echo.ErrUnauthorized
			}
			c.Request().Header.Set("user_id", strconv.Itoa(claims.ID))

			return claims, nil
		},
		TokenLookupFuncs: []echo_middleware.ValuesExtractor{
			func(c echo.Context) ([]string, error) {
				if len(c.Request().Header.Get(echo.HeaderAuthorization)) > 0 {
					return []string{c.Request().Header.Get(echo.HeaderAuthorization)}, nil
				}
				return []string{c.QueryParam("token")}, nil
			},
		},
	})
}

// readFormFile reads a multipart form file field, enforcing maxCertificateUploadSize.
func readFormFile(ctx echo.Context, field string) ([]byte, error) {
	fileHeader, err := ctx.FormFile(field)
	if err != nil {
		return nil, err
	}

	if fileHeader.Size > maxCertificateUploadSize {
		return nil, echo.NewHTTPError(http.StatusBadRequest, field+" file is too large")
	}

	file, err := fileHeader.Open()
	if err != nil {
		return nil, err
	}
	defer file.Close()

	return io.ReadAll(io.LimitReader(file, maxCertificateUploadSize))
}
