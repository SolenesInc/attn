import Capacitor
import Foundation
import Security

@objc(PushPlugin)
public class PushPlugin: CAPPlugin, CAPBridgedPlugin {
    public let identifier = "PushPlugin"
    public let jsName = "AttnPush"
    public let pluginMethods: [CAPPluginMethod] = [
        CAPPluginMethod(name: "prepare", returnType: CAPPluginReturnPromise)
    ]

    @objc func prepare(_ call: CAPPluginCall) {
        guard let encoded = call.getString("noiseSecret"), let secret = Data(base64URL: encoded), secret.count == 32 else {
            return call.reject("AttnPush.prepare needs the device's 32-byte Noise secret as base64url")
        }
        guard let group = Bundle.main.object(forInfoDictionaryKey: "AttnPushKeychainGroup") as? String else {
            return call.reject("Info.plist has no AttnPushKeychainGroup, so the notification extension cannot read the key")
        }
        let item: [CFString: Any] = [
            kSecClass: kSecClassGenericPassword,
            kSecAttrService: "codes.attn.mobile.push",
            kSecAttrAccount: "noise-secret",
            kSecAttrAccessGroup: group,
        ]
        SecItemDelete(item as CFDictionary)
        var attributes = item
        attributes[kSecValueData] = secret
        attributes[kSecAttrAccessible] = kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
        let status = SecItemAdd(attributes as CFDictionary, nil)
        guard status == errSecSuccess else {
            return call.reject("sharing the push key with the notification extension failed with OSStatus \(status)")
        }
        call.resolve(["environment": apnsEnvironment()])
    }

    private func apnsEnvironment() -> String {
        guard let url = Bundle.main.url(forResource: "embedded", withExtension: "mobileprovision"),
              let data = try? Data(contentsOf: url),
              let text = String(data: data, encoding: .isoLatin1),
              let start = text.range(of: "<?xml"),
              let end = text.range(of: "</plist>"),
              let profile = try? PropertyListSerialization.propertyList(from: Data(text[start.lowerBound..<end.upperBound].utf8), format: nil) as? [String: Any],
              let entitlements = profile["Entitlements"] as? [String: Any],
              (entitlements["aps-environment"] as? String) == "development"
        else { return "production" }
        return "sandbox"
    }
}

extension Data {
    init?(base64URL text: String) {
        var base64 = text.replacingOccurrences(of: "-", with: "+").replacingOccurrences(of: "_", with: "/")
        base64 += String(repeating: "=", count: (4 - base64.count % 4) % 4)
        self.init(base64Encoded: base64)
    }
}
