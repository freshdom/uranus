// SPDX-License-Identifier: AGPL-3.0-or-later
package web

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func index(context *gin.Context) {
	context.String(http.StatusOK, "200 ok")
}
