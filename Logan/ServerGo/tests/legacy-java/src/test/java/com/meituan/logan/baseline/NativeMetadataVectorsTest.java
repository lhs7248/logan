package com.meituan.logan.baseline;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.meituan.logan.web.parser.RequestContextParser;
import com.meituan.logan.web.model.LoganTaskModel;
import org.junit.Test;
import org.springframework.mock.web.MockHttpServletRequest;
import java.nio.file.*;
import java.util.*;

public class NativeMetadataVectorsTest {
 @Test public void generateMetadataAnswers()throws Exception{
  TimeZone before=TimeZone.getDefault();TimeZone.setDefault(TimeZone.getTimeZone("Asia/Shanghai"));
  try{
   List<Map<String,Object>> result=new ArrayList<>();
   String[][] inputs={{"2026-10-15","1"},{"2026-02-30","2"},{"2026-1-2suffix","99"},{"bad"," 1"},{"2026-10-15","2147483648"},{"2026-10-15","+1"},{"2026-10-15","-1"},{"2026-10-15",""},{"",""}};
   for(int i=0;i<inputs.length;i++){
    Map<String,String> headers=new LinkedHashMap<>();headers.put("filedate",inputs[i][0]);headers.put("platform",inputs[i][1]);
    if(i!=8){headers.put("appid","app");headers.put("deviceid",i==7?"":"device");}
    MockHttpServletRequest request=new MockHttpServletRequest();request.setContent(new byte[]{1,2,3});for(Map.Entry<String,String> e:headers.entrySet())request.addHeader(e.getKey(),e.getValue());
    LoganTaskModel task=RequestContextParser.parse(request);
    Map<String,Object> row=new LinkedHashMap<>();row.put("headers",headers);row.put("amount",task.getAmount());row.put("platform",task.getPlatform());row.put("log_date",task.getLogDate());row.put("file_name_prefix",task.getLogFileName().substring(0,task.getLogFileName().length()-40));result.add(row);
   }
   Path p=Paths.get("../contract/captures/java-controller/java-native-metadata.json");Files.createDirectories(p.getParent());new ObjectMapper().writerWithDefaultPrettyPrinter().writeValue(p.toFile(),result);
  }finally{TimeZone.setDefault(before);}
 }
}
