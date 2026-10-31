import Foundation

extension String {
    /// 查询串取值转义。`.urlQueryAllowed` 放行 `&`/`=`/`+`/`?`/`;`：
    /// 直接拼会让「A&B」变成两个参数、`+` 被解成空格、`;` 让 Go 的 `ParseQuery` 整条判无效。
    /// 逗号要保留（tags 用逗号拼成一个参数），别一起减掉。
    var urlQueryEscaped: String {
        let allowed = CharacterSet.urlQueryAllowed.subtracting(CharacterSet(charactersIn: "&=+?;"))
        return addingPercentEncoding(withAllowedCharacters: allowed) ?? self
    }
}
