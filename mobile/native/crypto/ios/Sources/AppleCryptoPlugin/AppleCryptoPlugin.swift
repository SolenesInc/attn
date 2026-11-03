import Capacitor
import Foundation

@objc(AppleCryptoPlugin)
public class AppleCryptoPlugin: CAPPlugin, CAPBridgedPlugin {
    public let identifier = "AppleCryptoPlugin"
    public let jsName = "AppleCrypto"
    public let pluginMethods: [CAPPluginMethod] = [
        CAPPluginMethod(name: "x25519PublicKey", returnType: CAPPluginReturnPromise),
        CAPPluginMethod(name: "x25519SharedSecret", returnType: CAPPluginReturnPromise),
        CAPPluginMethod(name: "seal", returnType: CAPPluginReturnPromise),
        CAPPluginMethod(name: "open", returnType: CAPPluginReturnPromise),
    ]

    private struct MissingField: Error, CustomStringConvertible {
        let description: String
    }

    private func bytes(_ call: CAPPluginCall, _ field: String) throws -> Data {
        guard let text = call.getString(field), let data = Data(base64Encoded: text) else {
            throw MissingField(description: "AppleCrypto.\(call.methodName) needs \(field) as base64")
        }
        return data
    }

    private func answer(_ call: CAPPluginCall, _ work: () throws -> Data) {
        do {
            call.resolve(["value": try work().base64EncodedString()])
        } catch {
            call.reject("AppleCrypto.\(call.methodName) failed: \(error)")
        }
    }

    @objc func x25519PublicKey(_ call: CAPPluginCall) {
        answer(call) { try AppleCrypto.x25519PublicKey(privateKey: bytes(call, "privateKey")) }
    }

    @objc func x25519SharedSecret(_ call: CAPPluginCall) {
        answer(call) {
            try AppleCrypto.x25519SharedSecret(privateKey: bytes(call, "privateKey"), publicKey: bytes(call, "publicKey"))
        }
    }

    @objc func seal(_ call: CAPPluginCall) {
        answer(call) {
            try AppleCrypto.seal(
                key: bytes(call, "key"), nonce: bytes(call, "nonce"), ad: bytes(call, "ad"),
                plaintext: bytes(call, "plaintext"))
        }
    }

    @objc func open(_ call: CAPPluginCall) {
        answer(call) {
            try AppleCrypto.open(
                key: bytes(call, "key"), nonce: bytes(call, "nonce"), ad: bytes(call, "ad"), sealed: bytes(call, "sealed"))
        }
    }
}
