package com.meituan.logan.baseline;
import com.alibaba.fastjson.JSON;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.meituan.logan.web.model.LoganLogItem;
import com.meituan.logan.web.dto.LoganLogDetailDTO;
import com.meituan.logan.web.handler.*;
import org.apache.commons.lang3.StringUtils;
import org.junit.Test;
import java.io.*;import java.nio.file.*;import java.util.*;
public class NativeRecordVectorsTest {
 @Test public void answers() throws Exception {
  List<Map<String,Object>> rows=new ArrayList<>();
  String[] inputs={"{\"c\":\"first\",\"c\":\"last\"}","{\"C\":\"first\",\"C\":\"last\"}","{\"c\":{\"z\":null,\"a\":0.0000001,\"b\":1.0,\"x\":-0.0}}","{\"C\":\"text\",\"F\":2,\"L\":42}","{\"c\":{\"x\":null}}","{\"c\":[1e2,-0,{\"b\":null}]}","{\"c\":\"first\",\"C\":\"last\"}","{\"C\":\"first\",\"c\":\"last\"}","{\"c\":{\"z\":1,\"a\":2,\"b\":3}}","{\"c\":1e2,\"f\":1e0,\"l\":1e2}","{\"c\":-0,\"f\":-0,\"l\":-0}","{\"c\":\"hello\\n<>&\\\"中\",\"f\":\"1\",\"l\":\"+42\"}","{\"c\":123,\"f\":2,\"l\":123}","{\"c\":true,\"f\":3.0,\"l\":1.5}","{\"c\":null,\"f\":null,\"l\":null}","{}","{\"c\":{\"a\":1},\"f\":99,\"l\":\"9223372036854775808\"}","{\"c\":[1,true],\"f\":\" 1\",\"l\":\" 2\"}","\r\n \t\u2003\r{\"c\":\"a\",\"f\":\"+3\",\"l\":\"-1\"}\r\n{}\n", "bad", "null", "[]", "{\"f\":\"2147483648\"}"};
  for(String input:inputs){Map<String,Object> row=new LinkedHashMap<>();row.put("input",input);List<Object> records=new ArrayList<>();boolean error=false;try(BufferedReader r=new BufferedReader(new StringReader(input))){String line;while((line=r.readLine())!=null){if(StringUtils.isBlank(line))continue;LoganLogDetailDTO d=JSON.parseObject(line,LoganLogItem.class).transferToDetail(0);Map<String,Object> rec=new LinkedHashMap<>();rec.put("Content",d.getContent());rec.put("LogType",d.getLogType());rec.put("LogTime",d.getLogTime());records.add(rec);}}catch(Exception e){error=true;}row.put("records",records);row.put("error",error);rows.add(row);}
  Map<String,Object> out=new LinkedHashMap<>();out.put("records",rows);List<Object> handlers=new ArrayList<>();for(int type:new int[]{0,1,2,3,99}){ContentHandler h=type==1?new LoganTypeOneHandler():type==2?new LoganTypeTwoHandler():type==3?new LoganTypeThreeHandler():new LoganTypeUnknownHandler();for(String c:new String[]{"", "<>&\"\\\n中文", "{\"a\":1}","  a\t "}){Map<String,Object> row=new LinkedHashMap<>();row.put("type",type);row.put("content",c);row.put("simple",h.getSimpleContent(c));row.put("format",h.getFormatContent(c));handlers.add(row);}}out.put("handlers",handlers);
  Path p=Paths.get("../contract/captures/java-controller/java-native-records.json");Files.createDirectories(p.getParent());new ObjectMapper().writerWithDefaultPrettyPrinter().writeValue(p.toFile(),out);
 }
}
