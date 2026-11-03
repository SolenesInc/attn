import Capacitor
import Foundation
import Security

@objc(KeychainPlugin)
public class KeychainPlugin: CAPPlugin, CAPBridgedPlugin {
    public let identifier = "KeychainPlugin"
    public let jsName = "Keychain"
    public let pluginMethods: [CAPPluginMethod] = [
        CAPPluginMethod(name: "get", returnType: CAPPluginReturnPromise),
        CAPPluginMethod(name: "set", returnType: CAPPluginReturnPromise)
    ]

    private func item(_ key: String) -> [CFString: Any] {
        [kSecClass: kSecClassGenericPassword, kSecAttrService: "codes.attn.mobile", kSecAttrAccount: key]
    }

    @objc func get(_ call: CAPPluginCall) {
        guard let key = call.getString("key") else { return call.reject("Keychain.get needs a key") }
        var query = item(key)
        query[kSecReturnData] = true
        query[kSecMatchLimit] = kSecMatchLimitOne
        var found: CFTypeRef?
        let status = SecItemCopyMatching(query as CFDictionary, &found)
        if status == errSecItemNotFound { return call.resolve([:]) }
        guard status == errSecSuccess, let data = found as? Data, let value = String(data: data, encoding: .utf8) else {
            return call.reject("reading \(key) from the Keychain failed with OSStatus \(status)")
        }
        call.resolve(["value": value])
    }

    @objc func set(_ call: CAPPluginCall) {
        guard let key = call.getString("key"), let value = call.getString("value") else {
            return call.reject("Keychain.set needs a key and a value")
        }
        SecItemDelete(item(key) as CFDictionary)
        var attributes = item(key)
        attributes[kSecValueData] = Data(value.utf8)
        attributes[kSecAttrAccessible] = kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
        let status = SecItemAdd(attributes as CFDictionary, nil)
        guard status == errSecSuccess else {
            return call.reject("writing \(key) to the Keychain failed with OSStatus \(status)")
        }
        call.resolve()
    }
}
