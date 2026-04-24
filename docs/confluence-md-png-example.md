# Confluence Markdown PNG Embed Test

下面这行是标准 Markdown 图片语法，图片使用相对路径引用同目录下的 `assets/` 文件。

![Confluence PNG embed test](assets/confluence-md-png-example.png "Confluence PNG embed test")

如果上传到 Confluence 后相对路径不能解析，可以把图片也上传为同一个页面的附件，然后把路径改成下面这种形式：

```md
![Confluence PNG embed test](confluence-md-png-example.png)
```

如果需要直接引用 Confluence 附件下载地址，可以换成下面这种形式，把 `PAGE_ID` 替换成页面 ID：

```md
![Confluence PNG embed test](/download/attachments/PAGE_ID/confluence-md-png-example.png?api=v2)
```
