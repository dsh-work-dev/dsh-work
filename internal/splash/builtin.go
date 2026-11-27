// Package splash owns the startup splash: built-in animated scenes drawn by the
// frontend and image or video files the user imports.
package splash

// BuiltinWhale is the default splash: the whale tail scene.
const BuiltinWhale = "builtin-whale"

// builtinIDs lists the frontend's built-in scenes. frontend/tests/splash.test.ts
// reads this line to keep it equal to frontend/src/splash/scene.ts.
var builtinIDs = []string{"builtin-whale"}

func isBuiltin(id string) bool {
	for _, builtin := range builtinIDs {
		if id == builtin {
			return true
		}
	}
	return false
}
