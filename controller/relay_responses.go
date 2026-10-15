package controller

import (
	"github.com/gin-gonic/gin"

	relaycontroller "github.com/songquanpeng/one-api/relay/controller"
)

// RelayResponses 透传 OpenAI /v1/responses 协议（含计费），实现见 relay/controller/responses.go
func RelayResponses(c *gin.Context) {
	relaycontroller.RelayResponses(c)
}
